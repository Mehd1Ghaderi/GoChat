package actors

import (
	"strconv"

	"github.com/anthdm/hollywood/actor"
)

// Pool routes each message, sender kept, to one of n workers in turn: the
// workers run in parallel and n bounds how many requests run at once.
type Pool struct {
	n        int
	producer actor.Producer
	opts     []actor.OptFunc
	workers  []*actor.PID
	next     int
}

func NewPool(n int, producer actor.Producer, opts ...actor.OptFunc) actor.Producer {
	return func() actor.Receiver {
		return &Pool{n: n, producer: producer, opts: opts}
	}
}

func (p *Pool) Receive(c *actor.Context) {
	switch c.Message().(type) {
	case actor.Initialized, actor.Stopped:
	case actor.Started:
		p.workers = make([]*actor.PID, p.n)
		for i := range p.workers {
			p.workers[i] = c.SpawnChild(p.producer, "worker", append([]actor.OptFunc{actor.WithID(strconv.Itoa(i))}, p.opts...)...)
		}
	default:
		if len(p.workers) == 0 {
			return
		}
		w := p.workers[p.next]
		p.next = (p.next + 1) % len(p.workers)
		c.Engine().SendWithSender(w, c.Message(), c.Sender())
	}
}
