// Package keylock serializes work by key within the process, over a fixed
// set of stripes: keys that share a stripe wait for each other.
package keylock

import (
	"context"
	"hash/maphash"
)

const stripes = 256

type Locks struct {
	seed    maphash.Seed
	stripes [stripes]chan struct{}
}

func New() *Locks {
	l := &Locks{seed: maphash.MakeSeed()}
	for i := range l.stripes {
		l.stripes[i] = make(chan struct{}, 1)
	}
	return l
}

// Lock holds key until the returned func is called, or fails when ctx ends
// first.
func (l *Locks) Lock(ctx context.Context, key string) (func(), error) {
	ch := l.stripes[maphash.String(l.seed, key)%stripes]
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
