package channels

import (
	"context"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/gocql/gocql"
	"go.uber.org/zap"
	"golang.org/x/time/rate"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/actors"
	"github.com/ssenerg/gochat/internal/authz"
	"github.com/ssenerg/gochat/internal/core/ids"
	"github.com/ssenerg/gochat/internal/core/models"
	"github.com/ssenerg/gochat/internal/core/validate"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/repos/scylla"
	"github.com/ssenerg/gochat/internal/services/convert"
	"github.com/ssenerg/gochat/internal/services/hub"
)

// DefaultChannel is the channel every team member stays in.
const DefaultChannel = "town-square"

type idleCheck struct{}

type channel struct {
	*Deps
	id        gocql.UUID
	ch        *models.Channel
	members   map[gocql.UUID]models.ChannelRole
	memberIDs []string
	bucket    int32
	hasBucket bool
	typing    map[gocql.UUID]time.Time
	posters   map[gocql.UUID]*rate.Limiter
	active    time.Time
	loaded    bool
	loadErr   error
	asked     bool
	handled   uint64
	repeater  *actor.SendRepeater
}

func newChannel(d *Deps, id gocql.UUID) actor.Producer {
	return func() actor.Receiver {
		return &channel{Deps: d, id: id}
	}
}

func (a *channel) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), a.Cfg.OpTimeout)
}

func (a *channel) Receive(c *actor.Context) {
	switch msg := c.Message().(type) {
	case actor.Started:
		// Loading waits for the first request: Started runs on the
		// manager's goroutine.
		a.active = time.Now()
		r := c.SendRepeat(c.PID(), idleCheck{}, a.Cfg.IdleTimeout/2)
		a.repeater = &r
		return
	case actor.Stopped:
		if a.repeater != nil {
			a.repeater.Stop()
		}
		return
	case actor.Initialized:
		return
	case idleCheck:
		if time.Since(a.active) >= a.Cfg.IdleTimeout {
			a.passivate(c)
		}
		return
	case Request:
		a.active = time.Now()
		a.handled++
		a.asked = false
		if !a.loaded {
			a.loaded = true
			a.load()
		}
		if a.loadErr != nil {
			actors.Reply(c, a.loadErr)
			a.passivate(c)
			return
		}
		a.handle(c, msg)
	}
}

func (a *channel) passivate(c *actor.Context) {
	if !a.asked {
		a.asked = true
		c.Send(c.Parent(), passivate{id: a.id, pid: c.PID(), handled: a.handled})
	}
}

func (a *channel) load() {
	ctx, cancel := a.ctx()
	defer cancel()
	a.typing = make(map[gocql.UUID]time.Time)
	a.posters = make(map[gocql.UUID]*rate.Limiter)
	ch, err := a.Repo.GetChannel(ctx, a.id)
	if err != nil {
		if ierrors.Is(err, ierrors.CodeNotFound) {
			a.loadErr = ierrors.NotFound("channel not found")
		} else {
			a.Logger.Error("failed to load channel", zap.String("channel", a.id.String()), zap.Error(err))
			a.loadErr = ierrors.Unavailable("channel is unavailable, try again")
		}
		return
	}
	members, err := a.Repo.ListChannelMembers(ctx, a.id)
	if err != nil {
		a.Logger.Error("failed to load channel members", zap.String("channel", a.id.String()), zap.Error(err))
		a.loadErr = ierrors.Unavailable("channel is unavailable, try again")
		return
	}
	bucket, has, err := a.Repo.LastMessageBucket(ctx, a.id)
	if err != nil {
		a.Logger.Error("failed to load channel bucket", zap.String("channel", a.id.String()), zap.Error(err))
		a.loadErr = ierrors.Unavailable("channel is unavailable, try again")
		return
	}
	a.ch = ch
	a.bucket, a.hasBucket = bucket, has
	a.members = make(map[gocql.UUID]models.ChannelRole, len(members))
	for _, m := range members {
		a.members[m.UserID] = m.Role
	}
	a.snapshot()
}

// snapshot rebuilds the recipient list. Deliver messages share it, so it is
// replaced, never modified.
func (a *channel) snapshot() {
	out := make([]string, 0, len(a.members))
	for id := range a.members {
		out = append(out, id.String())
	}
	a.memberIDs = out
}

func (a *channel) deliver(userIDs []string, ev *pb.Event) {
	if len(userIDs) > 0 {
		a.Engine.Send(a.Hub, hub.Deliver{UserIDs: userIDs, Event: ev})
	}
}

func (a *channel) handle(c *actor.Context, req Request) {
	var (
		out any
		err error
	)
	switch msg := req.(type) {
	case Access:
		ch := *a.ch
		out = &AccessResult{Channel: &ch, Role: a.members[msg.P.UserID]}
	case View:
		out, err = a.view(msg.P)
	case Post:
		out, err = a.post(msg)
	case Edit:
		out, err = a.edit(msg)
	case Delete:
		out, err = a.delete(msg)
	case History:
		err = a.history(c, msg)
	case GetMessage:
		err = a.getMessage(c, msg)
	case Typing:
		out, err = a.typingEvent(msg)
	case MarkRead:
		out, err = a.markRead(msg)
	case Join:
		out, err = a.join(msg)
	case Leave:
		out, err = a.leave(msg)
	case AddMember:
		out, err = a.addMember(msg)
	case RemoveMember:
		out, err = a.removeMember(msg)
	case Members:
		err = a.listMembers(c, msg)
	default:
		return
	}
	if err != nil {
		if e := ierrors.As(err); e.Code == ierrors.CodeInternal {
			a.Logger.Error("channel request failed", append(e.Fields(), zap.String("channel", a.id.String()))...)
		}
		actors.Reply(c, err)
		return
	}
	if out != nil {
		actors.Reply(c, out)
	}
}

func (a *channel) member(p *models.Principal) (models.ChannelRole, error) {
	role, ok := a.members[p.UserID]
	if !ok {
		if a.ch.Kind == models.ChannelPublic {
			return "", ierrors.New(ierrors.CodePermissionDenied, "join the channel first")
		}
		return "", ierrors.NotFound("channel not found")
	}
	return role, nil
}

func (a *channel) teamRole(ctx context.Context, userID gocql.UUID) (models.TeamRole, error) {
	if a.ch.Kind == models.ChannelDirect {
		return "", nil
	}
	tm, err := a.Repo.GetTeamMember(ctx, a.ch.TeamID, userID)
	if err != nil {
		if ierrors.Is(err, ierrors.CodeNotFound) {
			return "", nil
		}
		return "", err
	}
	return tm.Role, nil
}

func (a *channel) view(p *models.Principal) (*pb.Channel, error) {
	role, ok := a.members[p.UserID]
	if !ok && a.ch.Kind != models.ChannelPublic {
		return nil, ierrors.NotFound("channel not found")
	}
	if !ok {
		ctx, cancel := a.ctx()
		defer cancel()
		tr, err := a.teamRole(ctx, p.UserID)
		if err != nil {
			return nil, err
		}
		if tr == "" && p.Role != models.SystemRoleAdmin {
			return nil, ierrors.NotFound("channel not found")
		}
	}
	return a.channelFor(p.UserID, role)
}

// channelFor renders the channel as userID sees it.
func (a *channel) channelFor(userID gocql.UUID, role models.ChannelRole) (*pb.Channel, error) {
	var peer *pb.User
	if a.ch.Kind == models.ChannelDirect {
		for id := range a.members {
			if id != userID {
				ctx, cancel := a.ctx()
				u, err := a.Users.Get(ctx, id)
				cancel()
				if err != nil {
					return nil, err
				}
				peer = convert.PublicUser(u)
			}
		}
	}
	return convert.Channel(a.ch, role, gocql.UUID{}, peer), nil
}

func (a *channel) poster(userID gocql.UUID) *rate.Limiter {
	l := a.posters[userID]
	if l == nil {
		l = rate.NewLimiter(rate.Limit(a.Cfg.PostRate), a.Cfg.PostBurst)
		a.posters[userID] = l
	}
	return l
}

func parseIDs(raw []string) ([]gocql.UUID, error) {
	seen := make(map[gocql.UUID]bool, len(raw))
	out := make([]gocql.UUID, 0, len(raw))
	for _, s := range raw {
		id, ok := ids.Parse(s)
		if !ok {
			return nil, ierrors.InvalidArgument("invalid attachment id")
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

func (a *channel) post(msg Post) (*pb.Message, error) {
	role, err := a.member(msg.P)
	if err != nil {
		return nil, err
	}
	if !a.Authz.Can(authz.ObjMessage, authz.ActCreate, string(role), string(msg.P.Role)) {
		return nil, ierrors.PermissionDenied()
	}
	if !a.poster(msg.P.UserID).Allow() {
		return nil, ierrors.ResourceExhausted("you are posting too fast")
	}
	content, err := validate.Text("message", msg.Content, a.Cfg.MaxMessageLength)
	if err != nil {
		return nil, err
	}
	if len(msg.AttachmentIDs) > a.Cfg.MaxAttachments {
		return nil, ierrors.InvalidArgument("too many attachments")
	}
	attIDs, err := parseIDs(msg.AttachmentIDs)
	if err != nil {
		return nil, err
	}
	if content == "" && len(attIDs) == 0 {
		return nil, ierrors.InvalidArgument("message is empty")
	}

	ctx, cancel := a.ctx()
	defer cancel()
	atts, err := a.ownAttachments(ctx, msg.P.UserID, attIDs)
	if err != nil {
		return nil, err
	}
	id := ids.NewTime()
	m := &models.Message{
		ChannelID:   a.id,
		Bucket:      ids.BucketOf(id),
		ID:          id,
		UserID:      msg.P.UserID,
		Content:     content,
		Attachments: attIDs,
	}
	for i, att := range atts {
		ok, err := a.Repo.ClaimAttachment(ctx, att, id)
		if err == nil && !ok {
			err = ierrors.InvalidArgument("an attachment is no longer available")
		}
		if err != nil {
			a.unclaim(atts[:i], id)
			return nil, err
		}
	}
	newBucket := !a.hasBucket || m.Bucket != a.bucket
	if err := a.Repo.InsertMessage(ctx, m, newBucket); err != nil {
		// A message that may still land keeps its attachments.
		if !scylla.Uncertain(err) {
			a.unclaim(atts, id)
		}
		return nil, err
	}
	a.bucket, a.hasBucket = m.Bucket, true
	a.ch.LastMessageID = id
	if err := a.Repo.SetLastMessage(ctx, a.id, id); err != nil {
		a.Logger.Warn("failed to record the last message", zap.String("channel", a.id.String()), zap.Error(err))
	}
	if err := a.Repo.SetLastRead(ctx, msg.P.UserID, a.id, id); err != nil {
		a.Logger.Warn("failed to mark own message read", zap.Error(err))
	}
	delete(a.typing, msg.P.UserID)

	author, err := a.Users.Get(ctx, msg.P.UserID)
	if err != nil {
		return nil, err
	}
	out := convert.Message(m, convert.PublicUser(author), atts)
	a.deliver(a.memberIDs, &pb.Event{Payload: &pb.Event_MessagePosted{MessagePosted: &pb.MessagePosted{Message: out}}})
	return out, nil
}

// unclaim returns attachments claimed for a message that was not written to
// the uploads awaiting a post.
func (a *channel) unclaim(atts []*models.Attachment, messageID gocql.UUID) {
	ctx, cancel := a.ctx()
	defer cancel()
	for _, att := range atts {
		if err := a.Repo.UnclaimAttachment(ctx, att, messageID); err != nil {
			a.Logger.Warn("failed to release an attachment", zap.String("attachment", att.ID.String()), zap.Error(err))
		}
	}
}

// ownAttachments returns the attachments of attIDs, in order, refusing any
// not uploaded by userID to this channel or already posted.
func (a *channel) ownAttachments(ctx context.Context, userID gocql.UUID, attIDs []gocql.UUID) ([]*models.Attachment, error) {
	if len(attIDs) == 0 {
		return nil, nil
	}
	found, err := a.Repo.GetAttachments(ctx, attIDs)
	if err != nil {
		return nil, err
	}
	byID := make(map[gocql.UUID]*models.Attachment, len(found))
	for _, att := range found {
		byID[att.ID] = att
	}
	out := make([]*models.Attachment, 0, len(attIDs))
	for _, id := range attIDs {
		att := byID[id]
		if att == nil || att.ChannelID != a.id || att.UploaderID != userID || !ids.IsZero(att.MessageID) {
			return nil, ierrors.InvalidArgument("attachment is not available")
		}
		out = append(out, att)
	}
	return out, nil
}

func (a *channel) messageOf(ctx context.Context, raw string) (*models.Message, error) {
	id, ok := ids.ParseTime(raw)
	if !ok {
		return nil, ierrors.InvalidArgument("invalid message id")
	}
	m, err := a.Repo.GetMessage(ctx, a.id, id)
	if err != nil {
		if ierrors.Is(err, ierrors.CodeNotFound) {
			return nil, ierrors.NotFound("message not found")
		}
		return nil, err
	}
	return m, nil
}

func (a *channel) edit(msg Edit) (*pb.Message, error) {
	role, err := a.member(msg.P)
	if err != nil {
		return nil, err
	}
	if !a.Authz.Can(authz.ObjMessage, authz.ActEditOwn, string(role), string(msg.P.Role)) {
		return nil, ierrors.PermissionDenied()
	}
	content, err := validate.Text("message", msg.Content, a.Cfg.MaxMessageLength)
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.ctx()
	defer cancel()
	m, err := a.messageOf(ctx, msg.MessageID)
	if err != nil {
		return nil, err
	}
	if m.UserID != msg.P.UserID {
		return nil, ierrors.PermissionDenied()
	}
	if content == "" && len(m.Attachments) == 0 {
		return nil, ierrors.InvalidArgument("message is empty")
	}
	now := time.Now().UTC()
	if err := a.Repo.UpdateMessageContent(ctx, a.id, m.ID, content, now); err != nil {
		return nil, err
	}
	m.Content, m.EditedAt = content, now
	atts, err := a.Repo.GetAttachments(ctx, m.Attachments)
	if err != nil {
		return nil, err
	}
	author, err := a.Users.Get(ctx, m.UserID)
	if err != nil {
		return nil, err
	}
	out := convert.Message(m, convert.PublicUser(author), orderAttachments(m.Attachments, atts))
	a.deliver(a.memberIDs, &pb.Event{Payload: &pb.Event_MessageEdited{MessageEdited: &pb.MessageEdited{Message: out}}})
	return out, nil
}

func (a *channel) delete(msg Delete) (Done, error) {
	role, err := a.member(msg.P)
	if err != nil {
		return Done{}, err
	}
	ctx, cancel := a.ctx()
	defer cancel()
	m, err := a.messageOf(ctx, msg.MessageID)
	if err != nil {
		return Done{}, err
	}
	if m.UserID == msg.P.UserID {
		if !a.Authz.Can(authz.ObjMessage, authz.ActDeleteOwn, string(role), string(msg.P.Role)) {
			return Done{}, ierrors.PermissionDenied()
		}
	} else {
		tr, err := a.teamRole(ctx, msg.P.UserID)
		if err != nil {
			return Done{}, err
		}
		if !a.Authz.Can(authz.ObjMessage, authz.ActDeleteAny, string(role), string(tr), string(msg.P.Role)) {
			return Done{}, ierrors.PermissionDenied()
		}
	}
	if err := a.Repo.DeleteMessage(ctx, a.id, m.ID); err != nil {
		return Done{}, err
	}
	a.dropAttachments(m.Attachments)
	a.deliver(a.memberIDs, &pb.Event{Payload: &pb.Event_MessageDeleted{MessageDeleted: &pb.MessageDeleted{
		ChannelId: a.id.String(), MessageId: m.ID.String(),
	}}})
	return Done{}, nil
}

// dropAttachments deletes a deleted message's files in the background.
func (a *channel) dropAttachments(attIDs []gocql.UUID) {
	if len(attIDs) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		atts, err := a.Repo.GetAttachments(ctx, attIDs)
		if err != nil {
			a.Logger.Warn("failed to read attachments to delete", zap.Error(err))
			return
		}
		for _, att := range atts {
			if att.ChannelID != a.id {
				continue
			}
			if err := a.Storage.Delete(ctx, att.ObjectKey); err != nil {
				a.Logger.Warn("failed to delete attachment object", zap.String("attachment", att.ID.String()), zap.Error(err))
				continue
			}
			if err := a.Repo.DeleteAttachment(ctx, att); err != nil {
				a.Logger.Warn("failed to delete attachment", zap.String("attachment", att.ID.String()), zap.Error(err))
			}
		}
	}()
}

func orderAttachments(order []gocql.UUID, atts []*models.Attachment) []*models.Attachment {
	byID := make(map[gocql.UUID]*models.Attachment, len(atts))
	for _, att := range atts {
		byID[att.ID] = att
	}
	out := make([]*models.Attachment, 0, len(order))
	for _, id := range order {
		if att := byID[id]; att != nil {
			out = append(out, att)
		}
	}
	return out
}

func (a *channel) canRead(p *models.Principal) error {
	role, err := a.member(p)
	if err != nil {
		return err
	}
	if !a.Authz.Can(authz.ObjMessage, authz.ActRead, string(role), string(p.Role)) {
		return ierrors.PermissionDenied()
	}
	return nil
}

// history reads off the actor: the membership check happens here, in order,
// and the reply comes from the reading goroutine.
func (a *channel) history(c *actor.Context, msg History) error {
	if err := a.canRead(msg.P); err != nil {
		return err
	}
	var before gocql.UUID
	if msg.BeforeID != "" {
		id, ok := ids.ParseTime(msg.BeforeID)
		if !ok {
			return ierrors.InvalidArgument("invalid cursor")
		}
		before = id
	}
	limit := msg.Limit
	if limit <= 0 || limit > a.Cfg.HistoryLimit {
		limit = a.Cfg.HistoryLimit
	}
	a.async(c, func(ctx context.Context) (any, error) {
		list, more, err := a.Repo.ListMessages(ctx, a.id, before, limit)
		if err != nil {
			return nil, err
		}
		out, err := a.render(ctx, list)
		if err != nil {
			return nil, err
		}
		return &HistoryResult{Messages: out, HasMore: more}, nil
	})
	return nil
}

func (a *channel) getMessage(c *actor.Context, msg GetMessage) error {
	if err := a.canRead(msg.P); err != nil {
		return err
	}
	a.async(c, func(ctx context.Context) (any, error) {
		m, err := a.messageOf(ctx, msg.MessageID)
		if err != nil {
			return nil, err
		}
		out, err := a.render(ctx, []*models.Message{m})
		if err != nil {
			return nil, err
		}
		return out[0], nil
	})
	return nil
}

// render attaches authors and attachments to stored messages.
func (a *channel) render(ctx context.Context, list []*models.Message) ([]*pb.Message, error) {
	userIDs := make([]gocql.UUID, 0, len(list))
	var attIDs []gocql.UUID
	for _, m := range list {
		userIDs = append(userIDs, m.UserID)
		attIDs = append(attIDs, m.Attachments...)
	}
	authors, err := a.Users.GetMany(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	atts := make(map[gocql.UUID]*models.Attachment, len(attIDs))
	for start := 0; start < len(attIDs); start += 100 {
		found, err := a.Repo.GetAttachments(ctx, attIDs[start:min(start+100, len(attIDs))])
		if err != nil {
			return nil, err
		}
		for _, att := range found {
			atts[att.ID] = att
		}
	}
	out := make([]*pb.Message, 0, len(list))
	for _, m := range list {
		var mine []*models.Attachment
		for _, id := range m.Attachments {
			if att := atts[id]; att != nil {
				mine = append(mine, att)
			}
		}
		out = append(out, convert.Message(m, convert.PublicUser(authors[m.UserID]), mine))
	}
	return out, nil
}

func (a *channel) typingEvent(msg Typing) (Done, error) {
	if _, err := a.member(msg.P); err != nil {
		return Done{}, err
	}
	now := time.Now()
	if last, ok := a.typing[msg.P.UserID]; ok && now.Sub(last) < a.Cfg.TypingInterval {
		return Done{}, nil
	}
	a.typing[msg.P.UserID] = now
	ctx, cancel := a.ctx()
	defer cancel()
	u, err := a.Users.Get(ctx, msg.P.UserID)
	if err != nil {
		return Done{}, err
	}
	self := msg.P.UserID.String()
	others := make([]string, 0, len(a.memberIDs))
	for _, id := range a.memberIDs {
		if id != self {
			others = append(others, id)
		}
	}
	a.deliver(others, &pb.Event{Payload: &pb.Event_Typing{Typing: &pb.Typing{ChannelId: a.id.String(), User: convert.PublicUser(u)}}})
	return Done{}, nil
}

func (a *channel) markRead(msg MarkRead) (Done, error) {
	if _, err := a.member(msg.P); err != nil {
		return Done{}, err
	}
	id, ok := ids.ParseTime(msg.MessageID)
	if !ok || id.Time().After(time.Now().Add(time.Minute)) {
		return Done{}, ierrors.InvalidArgument("invalid message id")
	}
	ctx, cancel := a.ctx()
	defer cancel()
	return Done{}, a.Repo.SetLastRead(ctx, msg.P.UserID, a.id, id)
}

func (a *channel) addLocal(ctx context.Context, userID gocql.UUID, role models.ChannelRole) error {
	m := &models.ChannelMember{ChannelID: a.id, UserID: userID, TeamID: a.ch.TeamID, Role: role, JoinedAt: time.Now().UTC()}
	if err := a.Repo.AddChannelMember(ctx, m); err != nil {
		return err
	}
	a.members[userID] = role
	a.snapshot()
	return nil
}

func (a *channel) removeLocal(ctx context.Context, userID gocql.UUID) error {
	m := &models.ChannelMember{ChannelID: a.id, UserID: userID, TeamID: a.ch.TeamID}
	if err := a.Repo.RemoveChannelMember(ctx, m); err != nil {
		return err
	}
	delete(a.members, userID)
	delete(a.typing, userID)
	delete(a.posters, userID)
	a.snapshot()
	return nil
}

func (a *channel) joined(userID gocql.UUID, role models.ChannelRole) (*pb.Channel, error) {
	view, err := a.channelFor(userID, role)
	if err != nil {
		return nil, err
	}
	a.deliver([]string{userID.String()}, &pb.Event{Payload: &pb.Event_ChannelJoined{ChannelJoined: &pb.ChannelJoined{Channel: view}}})
	return view, nil
}

func (a *channel) left(userID gocql.UUID) {
	a.deliver([]string{userID.String()}, &pb.Event{Payload: &pb.Event_ChannelLeft{ChannelLeft: &pb.ChannelLeft{
		ChannelId: a.id.String(), TeamId: convert.ID(a.ch.TeamID),
	}}})
}

func (a *channel) join(msg Join) (*pb.Channel, error) {
	if role, ok := a.members[msg.P.UserID]; ok {
		return a.channelFor(msg.P.UserID, role)
	}
	if a.ch.Kind != models.ChannelPublic {
		return nil, ierrors.NotFound("channel not found")
	}
	ctx, cancel := a.ctx()
	defer cancel()
	tr, err := a.teamRole(ctx, msg.P.UserID)
	if err != nil {
		return nil, err
	}
	if tr == "" {
		return nil, ierrors.NotFound("channel not found")
	}
	if !msg.System && !a.Authz.Can(authz.ObjChannel, authz.ActJoin, string(tr), string(msg.P.Role)) {
		return nil, ierrors.PermissionDenied()
	}
	if err := a.addLocal(ctx, msg.P.UserID, models.ChannelRoleMember); err != nil {
		return nil, err
	}
	return a.joined(msg.P.UserID, models.ChannelRoleMember)
}

func (a *channel) leave(msg Leave) (Done, error) {
	if _, err := a.member(msg.P); err != nil {
		return Done{}, err
	}
	if a.ch.Kind == models.ChannelDirect {
		return Done{}, ierrors.FailedPrecondition("direct conversations cannot be left")
	}
	if a.ch.Name == DefaultChannel {
		return Done{}, ierrors.FailedPrecondition("every team member stays in this channel")
	}
	ctx, cancel := a.ctx()
	defer cancel()
	if err := a.removeLocal(ctx, msg.P.UserID); err != nil {
		return Done{}, err
	}
	a.left(msg.P.UserID)
	return Done{}, nil
}

func (a *channel) addMember(msg AddMember) (Done, error) {
	role, err := a.member(msg.P)
	if err != nil {
		return Done{}, err
	}
	if a.ch.Kind == models.ChannelDirect {
		return Done{}, ierrors.FailedPrecondition("direct conversations have two members")
	}
	if !a.Authz.Can(authz.ObjChannelMember, authz.ActAdd, string(role), string(msg.P.Role)) {
		return Done{}, ierrors.PermissionDenied()
	}
	target, ok := ids.Parse(msg.UserID)
	if !ok {
		return Done{}, ierrors.InvalidArgument("invalid user id")
	}
	if _, exists := a.members[target]; exists {
		return Done{}, nil
	}
	ctx, cancel := a.ctx()
	defer cancel()
	tr, err := a.teamRole(ctx, target)
	if err != nil {
		return Done{}, err
	}
	if tr == "" {
		return Done{}, ierrors.InvalidArgument("only team members can be added")
	}
	if err := a.addLocal(ctx, target, models.ChannelRoleMember); err != nil {
		return Done{}, err
	}
	if _, err := a.joined(target, models.ChannelRoleMember); err != nil {
		return Done{}, err
	}
	return Done{}, nil
}

func (a *channel) removeMember(msg RemoveMember) (Done, error) {
	if _, ok := a.members[msg.UserID]; !ok {
		if msg.System {
			return Done{}, nil
		}
		return Done{}, ierrors.NotFound("not a member")
	}
	if a.ch.Kind == models.ChannelDirect && !msg.System {
		return Done{}, ierrors.FailedPrecondition("direct conversations have two members")
	}
	ctx, cancel := a.ctx()
	defer cancel()
	if !msg.System {
		role := a.members[msg.P.UserID]
		tr, err := a.teamRole(ctx, msg.P.UserID)
		if err != nil {
			return Done{}, err
		}
		if !a.Authz.Can(authz.ObjChannelMember, authz.ActRemove, string(role), string(tr), string(msg.P.Role)) {
			return Done{}, ierrors.PermissionDenied()
		}
		if a.ch.Name == DefaultChannel {
			return Done{}, ierrors.FailedPrecondition("every team member stays in this channel")
		}
	}
	if err := a.removeLocal(ctx, msg.UserID); err != nil {
		return Done{}, err
	}
	a.left(msg.UserID)
	return Done{}, nil
}

func (a *channel) listMembers(c *actor.Context, msg Members) error {
	if _, err := a.member(msg.P); err != nil {
		return err
	}
	roles := make(map[gocql.UUID]models.ChannelRole, len(a.members))
	userIDs := make([]gocql.UUID, 0, len(a.members))
	for id, role := range a.members {
		roles[id] = role
		userIDs = append(userIDs, id)
	}
	a.async(c, func(ctx context.Context) (any, error) {
		found, err := a.Users.GetMany(ctx, userIDs)
		if err != nil {
			return nil, err
		}
		out := make([]*pb.ChannelMember, 0, len(found))
		for id, u := range found {
			out = append(out, &pb.ChannelMember{User: convert.PublicUser(u), Role: convert.ChannelRole(roles[id])})
		}
		return out, nil
	})
	return nil
}

// async runs a read off the actor and replies from its goroutine. fn may only
// touch what does not change after the actor loaded.
func (a *channel) async(c *actor.Context, fn func(ctx context.Context) (any, error)) {
	sender, engine := c.Sender(), c.Engine()
	if !actors.IsAsk(sender) {
		return
	}
	go func() {
		ctx, cancel := a.ctx()
		defer cancel()
		out, err := fn(ctx)
		if err != nil {
			if e := ierrors.As(err); e.Code == ierrors.CodeInternal {
				a.Logger.Error("channel read failed", append(e.Fields(), zap.String("channel", a.id.String()))...)
			}
			engine.Send(sender, err)
			return
		}
		engine.Send(sender, out)
	}()
}
