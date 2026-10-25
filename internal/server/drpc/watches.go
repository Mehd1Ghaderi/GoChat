package drpc

import (
	"sync"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
)

// maxWatchChecks bounds the Watch checks the directory runs at once, for
// every gateway together.
const maxWatchChecks = 4

// watchChecks runs a gateway's Watch frames off its stream, one at a time
// for each connection: a frame that comes while its connection's check
// runs waits, in place of any frame waiting before it, so a flood of
// frames costs two checks, not one each.
type watchChecks struct {
	check func(w *pb.Watch, seq uint64)

	mu  sync.Mutex
	seq uint64
	// waiting holds, for each connection whose check runs, the frame that
	// comes next; nil when none does.
	waiting map[uint64]*pb.Watch
}

func newWatchChecks(check func(w *pb.Watch, seq uint64)) *watchChecks {
	return &watchChecks{check: check, waiting: make(map[uint64]*pb.Watch)}
}

func (c *watchChecks) add(w *pb.Watch) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, running := c.waiting[w.ConnId]; running {
		c.waiting[w.ConnId] = w
		return
	}
	c.waiting[w.ConnId] = nil
	c.seq++
	go c.run(w, c.seq)
}

func (c *watchChecks) run(w *pb.Watch, seq uint64) {
	for {
		c.check(w, seq)
		c.mu.Lock()
		next := c.waiting[w.ConnId]
		if next == nil {
			delete(c.waiting, w.ConnId)
			c.mu.Unlock()
			return
		}
		c.waiting[w.ConnId] = nil
		c.seq++
		w, seq = next, c.seq
		c.mu.Unlock()
	}
}
