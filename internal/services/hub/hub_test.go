package hub

import (
	"testing"
	"time"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
)

func TestStatus(t *testing.T) {
	h := &hub{online: map[string]map[string]int{"a": {"gw": 1}}, chosen: map[string]choice{}}
	cases := []struct {
		chosen choice
		want   pb.Status
	}{
		{choice{}, pb.Status_STATUS_ONLINE},
		{choice{status: pb.Status_STATUS_AWAY}, pb.Status_STATUS_AWAY},
		{choice{status: pb.Status_STATUS_DND}, pb.Status_STATUS_DND},
		{choice{status: pb.Status_STATUS_DND, dndUntil: time.Now().Add(time.Hour)}, pb.Status_STATUS_DND},
		{choice{status: pb.Status_STATUS_DND, dndUntil: time.Now().Add(-time.Second)}, pb.Status_STATUS_ONLINE},
		{choice{status: pb.Status_STATUS_OFFLINE}, pb.Status_STATUS_OFFLINE},
	}
	for _, c := range cases {
		h.chosen["a"] = c.chosen
		if got := h.status("a"); got != c.want {
			t.Errorf("%+v: got %v, want %v", c.chosen, got, c.want)
		}
	}
	if got := h.status("b"); got != pb.Status_STATUS_OFFLINE {
		t.Errorf("a user with no connection is %v", got)
	}
}
