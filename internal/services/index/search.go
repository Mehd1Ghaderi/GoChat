package index

import (
	"bytes"
	"context"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/anthdm/hollywood/actor"
	"github.com/gocql/gocql"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/actors"
	"github.com/ssenerg/gochat/internal/core/ids"
	"github.com/ssenerg/gochat/internal/core/models"
	"github.com/ssenerg/gochat/internal/core/search"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/repos/scylla"
	"github.com/ssenerg/gochat/internal/services/convert"
)

// Search finds the messages of a team, and of the searcher's direct
// conversations, that match Query, newest first. It replies with *Result.
type Search struct {
	P      *models.Principal
	TeamID string
	Query  string
	Cursor string
	Limit  int
}

type Result struct {
	Hits []*pb.SearchHit
	// Terms are the words looked for, to mark in the results.
	Terms []string
	// Next continues the search; empty at its end.
	Next string
}

const (
	defaultLimit = 20
	maxLimit     = 50
	pageSize     = 100
	// A search stops after this many reads of the index or checks of a
	// message and offers to go on.
	maxReads  = 60
	maxChecks = 240
	maxMonths = 120
	checkers  = 8
)

func Searcher(d *Deps) actor.Producer {
	d.limits = newLimiter(rate.Limit(d.Cfg.Rate), d.Cfg.Burst)
	return actors.NewPool(d.Cfg.Workers, func() actor.Receiver { return &searcher{Deps: d} },
		actor.WithMiddleware(actors.Recover(d.Logger)))
}

type searcher struct{ *Deps }

func (s *searcher) Receive(c *actor.Context) {
	msg, ok := c.Message().(Search)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.Cfg.OpTimeout)
	defer cancel()
	out, err := s.search(ctx, msg)
	if err != nil {
		if e := ierrors.As(err); e.Code == ierrors.CodeInternal {
			s.Logger.Error("search failed", e.Fields()...)
		}
		actors.Reply(c, err)
		return
	}
	actors.Reply(c, out)
}

func (s *searcher) search(ctx context.Context, msg Search) (*Result, error) {
	if !s.limits.allow(msg.P.UserID) {
		return nil, ierrors.ResourceExhausted("you are searching too fast, wait a moment")
	}
	q, err := search.Parse(msg.Query)
	if err != nil {
		return nil, err
	}
	teamID, ok := ids.Parse(msg.TeamID)
	if !ok {
		return nil, ierrors.InvalidArgument("invalid team id")
	}
	if _, err := s.Repo.GetTeamMember(ctx, teamID, msg.P.UserID); err != nil {
		if ierrors.Is(err, ierrors.CodeNotFound) {
			return nil, ierrors.NotFound("team not found")
		}
		return nil, err
	}
	var cursor gocql.UUID
	if msg.Cursor != "" {
		if cursor, ok = ids.ParseTime(msg.Cursor); !ok {
			return nil, ierrors.InvalidArgument("invalid cursor")
		}
	}
	r := &run{searcher: s, q: q, limit: msg.Limit}
	if r.limit <= 0 || r.limit > maxLimit {
		r.limit = defaultLimit
	}
	out := &Result{Terms: q.Terms}
	if err := r.prepare(ctx, msg.P, teamID); err != nil || r.empty {
		return out, err
	}
	if len(q.Terms) == 0 {
		err = r.history(ctx, cursor)
	} else {
		err = r.index(ctx, cursor)
	}
	if err != nil {
		return nil, err
	}
	found := r.found[:min(r.limit, len(r.found))]
	if out.Hits, err = s.hits(ctx, found, msg.P.UserID); err != nil {
		return nil, err
	}
	out.Next = r.next()
	return out, nil
}

// run is one search under way.
type run struct {
	*searcher
	q       *search.Query
	limit   int
	allowed map[gocql.UUID]bool
	only    gocql.UUID
	from    gocql.UUID
	scopes  []gocql.UUID
	// first and last are the newest and oldest months to read.
	first, last int32
	found       []*models.Message
	reads       int
	checks      int
	// stop is where a search cut short goes on.
	stop  gocql.UUID
	empty bool
}

func (r *run) prepare(ctx context.Context, p *models.Principal, teamID gocql.UUID) error {
	mine, err := r.Repo.ListUserChannels(ctx, p.UserID, teamID)
	if err != nil {
		return err
	}
	direct, err := r.Repo.ListUserChannels(ctx, p.UserID, scylla.NoTeam)
	if err != nil {
		return err
	}
	r.allowed = make(map[gocql.UUID]bool, len(mine)+len(direct))
	for _, m := range append(mine, direct...) {
		r.allowed[m.ChannelID] = true
	}
	r.scopes = []gocql.UUID{teamID, p.UserID}
	if r.q.In != "" {
		id, err := r.Repo.GetChannelIDByName(ctx, teamID, r.q.In)
		if ierrors.Is(err, ierrors.CodeNotFound) || (err == nil && !r.allowed[id]) {
			r.empty = true
			return nil
		}
		if err != nil {
			return err
		}
		r.only, r.scopes = id, []gocql.UUID{teamID}
	}
	if r.q.From != "" {
		id, err := r.Repo.GetUserIDByUsername(ctx, r.q.From)
		if ierrors.Is(err, ierrors.CodeNotFound) {
			r.empty = true
			return nil
		}
		if err != nil {
			return err
		}
		r.from = id
	}
	team, err := r.Repo.GetTeam(ctx, teamID)
	if err != nil {
		return err
	}
	user, err := r.Users.Get(ctx, p.UserID)
	if err != nil {
		return err
	}
	oldest := team.CreatedAt
	if user.CreatedAt.Before(oldest) {
		oldest = user.CreatedAt
	}
	if r.q.After.After(oldest) {
		oldest = r.q.After
	}
	newest := time.Now()
	if !r.q.Before.IsZero() && r.q.Before.Before(newest) {
		newest = r.q.Before.Add(-time.Nanosecond)
	}
	r.first, r.last = scylla.Month(newest), scylla.Month(oldest)
	r.last = max(r.last, r.first-maxMonths)
	return nil
}

// keep reports whether a message may be shown and passes the filters.
func (r *run) keep(channelID, userID, id gocql.UUID) bool {
	if !r.allowed[channelID] || (!ids.IsZero(r.only) && channelID != r.only) || (!ids.IsZero(r.from) && userID != r.from) {
		return false
	}
	t := id.Time()
	return (r.q.After.IsZero() || !t.Before(r.q.After)) && (r.q.Before.IsZero() || t.Before(r.q.Before))
}

func (r *run) spent() bool {
	return r.reads >= maxReads || r.checks >= maxChecks
}

func (r *run) next() string {
	if len(r.found) > r.limit {
		return r.found[r.limit-1].ID.String()
	}
	if !ids.IsZero(r.stop) {
		return r.stop.String()
	}
	return ""
}

// history walks a channel when the search names it and no word.
func (r *run) history(ctx context.Context, cursor gocql.UUID) error {
	before := cursor
	if ids.IsZero(before) && !r.q.Before.IsZero() {
		before = gocql.MinTimeUUID(r.q.Before)
	}
	for !r.spent() {
		list, more, err := r.Repo.ListMessages(ctx, r.only, before, pageSize)
		if err != nil {
			return err
		}
		r.reads++
		for _, m := range list {
			before = m.ID
			if !r.q.After.IsZero() && m.ID.Time().Before(r.q.After) {
				return nil
			}
			if r.keep(m.ChannelID, m.UserID, m.ID) {
				if r.found = append(r.found, m); len(r.found) > r.limit {
					return nil
				}
			}
		}
		if !more {
			return nil
		}
	}
	r.stop = before
	return nil
}

// tagged is a posting and the scope it was read from.
type tagged struct {
	scylla.Posting
	scope gocql.UUID
}

// stream reads one scope's postings of a term in a month, newest first.
type stream struct {
	scope  gocql.UUID
	before gocql.UUID
	buf    []scylla.Posting
	done   bool
}

func (r *run) index(ctx context.Context, cursor gocql.UUID) error {
	driver, others := pickDriver(r.q.Terms)
	first := r.first
	if !ids.IsZero(cursor) {
		first = min(first, scylla.Month(cursor.Time()))
	}
	for month := first; month >= r.last; month-- {
		var bound gocql.UUID
		if !ids.IsZero(cursor) && month == scylla.Month(cursor.Time()) {
			bound = cursor
		}
		done, err := r.month(ctx, driver, others, month, bound)
		if err != nil || done {
			return err
		}
		if r.spent() && month > r.last {
			r.stop = gocql.MinTimeUUID(scylla.MonthStart(month))
			return nil
		}
	}
	return nil
}

// pickDriver reads the longest term, likely the rarest, and checks the others
// against it.
func pickDriver(terms []string) (string, []string) {
	best := 0
	for i, t := range terms {
		if utf8.RuneCountInString(t) > utf8.RuneCountInString(terms[best]) {
			best = i
		}
	}
	others := make([]string, 0, len(terms)-1)
	for i, t := range terms {
		if i != best {
			others = append(others, t)
		}
	}
	return terms[best], others
}

// month searches one month and reports whether the search is over.
func (r *run) month(ctx context.Context, driver string, others []string, month int32, bound gocql.UUID) (bool, error) {
	streams := make([]*stream, len(r.scopes))
	for i, scope := range r.scopes {
		streams[i] = &stream{scope: scope, before: bound}
	}
	for {
		batch, err := r.take(ctx, streams, driver, month)
		if err != nil || len(batch) == 0 {
			return false, err
		}
		pos := batch[len(batch)-1].MessageID
		cands := batch[:0]
		for _, t := range batch {
			if r.keep(t.ChannelID, t.UserID, t.MessageID) {
				cands = append(cands, t)
			}
		}
		if cands, err = r.intersect(ctx, cands, others, month); err != nil {
			return false, err
		}
		if err := r.verify(ctx, cands); err != nil {
			return false, err
		}
		if len(r.found) > r.limit {
			return true, nil
		}
		if r.spent() {
			r.stop = pos
			return true, nil
		}
	}
}

// take merges the scopes' postings into one page, newest first.
func (r *run) take(ctx context.Context, streams []*stream, term string, month int32) ([]tagged, error) {
	var out []tagged
	for len(out) < pageSize {
		var best *stream
		for _, s := range streams {
			if len(s.buf) == 0 && !s.done {
				page, err := r.Repo.Postings(ctx, s.scope, term, month, s.before, pageSize)
				if err != nil {
					return nil, err
				}
				r.reads++
				s.buf, s.done = page, len(page) < pageSize
				if len(page) > 0 {
					s.before = page[len(page)-1].MessageID
				}
			}
			if len(s.buf) > 0 && (best == nil || newer(s.buf[0].MessageID, best.buf[0].MessageID)) {
				best = s
			}
		}
		if best == nil {
			break
		}
		out = append(out, tagged{Posting: best.buf[0], scope: best.scope})
		best.buf = best.buf[1:]
	}
	return out, nil
}

func newer(a, b gocql.UUID) bool {
	if ta, tb := a.Timestamp(), b.Timestamp(); ta != tb {
		return ta > tb
	}
	return bytes.Compare(a[:], b[:]) > 0
}

// intersect keeps the candidates also filed under every other term.
func (r *run) intersect(ctx context.Context, cands []tagged, others []string, month int32) ([]tagged, error) {
	for _, term := range others {
		if len(cands) == 0 {
			break
		}
		byScope := map[gocql.UUID][]gocql.UUID{}
		for _, c := range cands {
			byScope[c.scope] = append(byScope[c.scope], c.MessageID)
		}
		has := make(map[gocql.UUID]map[gocql.UUID]bool, len(byScope))
		for scope, list := range byScope {
			found, err := r.Repo.HasPostings(ctx, scope, term, month, list)
			if err != nil {
				return nil, err
			}
			r.reads++
			has[scope] = found
		}
		kept := make([]tagged, 0, len(cands))
		for _, c := range cands {
			if has[c.scope][c.MessageID] {
				kept = append(kept, c)
			}
		}
		cands = kept
	}
	return cands, nil
}

// verify reads the candidates and keeps those still there and still
// matching: the index may lag behind an edit or a deletion.
func (r *run) verify(ctx context.Context, cands []tagged) error {
	for start := 0; start < len(cands) && len(r.found) <= r.limit; start += 2 * checkers {
		chunk := cands[start:min(start+2*checkers, len(cands))]
		msgs := make([]*models.Message, len(chunk))
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(checkers)
		for i, c := range chunk {
			g.Go(func() error {
				m, err := r.load(gctx, c.Posting)
				if ierrors.Is(err, ierrors.CodeNotFound) {
					return nil
				}
				if err != nil {
					return err
				}
				if m.ChannelID == c.ChannelID && r.q.Match(m.Content) {
					msgs[i] = m
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return err
		}
		r.checks += len(chunk)
		for _, m := range msgs {
			if m == nil {
				continue
			}
			if r.found = append(r.found, m); len(r.found) > r.limit {
				break
			}
		}
	}
	return nil
}

func (r *run) load(ctx context.Context, p scylla.Posting) (*models.Message, error) {
	if ids.IsZero(p.RootID) {
		return r.Repo.GetMessage(ctx, p.ChannelID, p.MessageID)
	}
	return r.Repo.GetReply(ctx, p.ChannelID, p.RootID, p.MessageID)
}

// hits renders the messages found with their authors and channels.
func (s *searcher) hits(ctx context.Context, found []*models.Message, self gocql.UUID) ([]*pb.SearchHit, error) {
	if len(found) == 0 {
		return nil, nil
	}
	var userIDs, chIDs []gocql.UUID
	seen := map[gocql.UUID]bool{}
	for _, m := range found {
		userIDs = append(userIDs, m.UserID)
		if !seen[m.ChannelID] {
			seen[m.ChannelID] = true
			chIDs = append(chIDs, m.ChannelID)
		}
	}
	chans, err := s.Repo.GetChannels(ctx, chIDs)
	if err != nil {
		return nil, err
	}
	for _, ch := range chans {
		userIDs = append(userIDs, convert.PeerIDs(ch, self)...)
	}
	people, err := s.Users.GetMany(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	views := make(map[gocql.UUID]*pb.Channel, len(chans))
	for _, ch := range chans {
		views[ch.ID] = convert.Conversation(ch, "", gocql.UUID{}, self, people)
	}
	out := make([]*pb.SearchHit, 0, len(found))
	for _, m := range found {
		if ch := views[m.ChannelID]; ch != nil {
			out = append(out, &pb.SearchHit{Message: convert.Message(m, convert.PublicUser(people[m.UserID]), nil), Channel: ch})
		}
	}
	return out, nil
}

// limiter holds each searcher's own rate.
type limiter struct {
	mu    sync.Mutex
	rate  rate.Limit
	burst int
	users map[gocql.UUID]*rate.Limiter
}

func newLimiter(r rate.Limit, burst int) *limiter {
	return &limiter{rate: r, burst: burst, users: make(map[gocql.UUID]*rate.Limiter)}
}

const maxLimiters = 100_000

func (l *limiter) allow(id gocql.UUID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	lim := l.users[id]
	if lim == nil {
		if len(l.users) >= maxLimiters {
			l.users = make(map[gocql.UUID]*rate.Limiter)
		}
		lim = rate.NewLimiter(l.rate, l.burst)
		l.users[id] = lim
	}
	return lim.Allow()
}
