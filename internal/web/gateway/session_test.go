package gateway

import (
	"strings"
	"testing"

	"go.uber.org/zap"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/web/render"
)

// quietSession is a session whose frames stay in its outbox.
func quietSession(t *testing.T) *session {
	t.Helper()
	r, err := render.New(100)
	if err != nil {
		t.Fatal(err)
	}
	return &session{
		Deps: &Deps{Logger: zap.NewNop(), Cfg: testConfig(), Render: r},
		out:  newOutbox(1 << 20), user: &pb.User{Id: "me", Username: "me"},
		channelID: "here", sidebar: map[string]bool{"here": true, "there": true},
		mentions: map[string]int{}, unread: map[string]bool{}, muted: map[string]bool{},
		directs: map[string]bool{}, names: map[string]string{}, peers: map[string]bool{}, peerOf: map[string]string{},
		saved: map[string]bool{}, reads: map[string]string{},
	}
}

func (s *session) frames() []string {
	items, _, _ := s.out.take()
	out := make([]string, len(items))
	for i, b := range items {
		out[i] = string(b)
	}
	return out
}

func posted(id, channel string) *pb.Event {
	return &pb.Event{Payload: &pb.Event_MessagePosted{MessagePosted: &pb.MessagePosted{Message: &pb.Message{
		Id: id, ChannelId: channel, Content: "hi", Author: &pb.User{Id: "someone", Username: "someone"},
	}}}}
}

func TestUnreadOnceUntilRead(t *testing.T) {
	s := quietSession(t)
	for _, id := range []string{"1", "2", "3"} {
		s.event(nil, posted(id, "there"))
	}
	if got := s.frames(); len(got) != 1 || !strings.Contains(got[0], `id="unread-there"`) {
		t.Fatalf("frames %q", got)
	}
	// Read, the channel is marked again by its next message.
	delete(s.unread, "there")
	s.event(nil, posted("4", "there"))
	if got := s.frames(); len(got) != 1 {
		t.Fatalf("frames after reading %q", got)
	}
}
