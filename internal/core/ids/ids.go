// Package ids makes the identifiers of stored entities: random UUIDs for
// entities, and time UUIDs, which sort by creation, for messages.
package ids

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/gocql/gocql"
)

// gregorianOffset is the number of 100ns intervals between 1582-10-15 and the
// Unix epoch.
const gregorianOffset = 0x01B21DD213814000

// BucketSpan is the time range of one message partition.
const BucketSpan = 10 * 24 * time.Hour

func New() gocql.UUID {
	u, err := gocql.RandomUUID()
	if err != nil {
		panic(err)
	}
	return u
}

type timeGen struct {
	mu    sync.Mutex
	last  int64
	clock uint32
	node  [6]byte
}

var gen = func() *timeGen {
	g := &timeGen{}
	_, _ = rand.Read(g.node[:])
	g.node[0] |= 0x01 // multicast bit: a random, not a hardware, node
	var c [2]byte
	_, _ = rand.Read(c[:])
	g.clock = uint32(c[0])<<8 | uint32(c[1])
	return g
}()

// NewTime returns a version 1 UUID with a random node. Successive calls in one
// process return strictly increasing timestamps.
func NewTime() gocql.UUID {
	return gen.next(time.Now())
}

func (g *timeGen) next(now time.Time) gocql.UUID {
	ts := now.UnixNano()/100 + gregorianOffset
	g.mu.Lock()
	if ts <= g.last {
		ts = g.last + 1
	}
	g.last = ts
	g.mu.Unlock()
	return gocql.TimeUUIDWith(ts, g.clock, g.node[:])
}

func Parse(s string) (gocql.UUID, bool) {
	if len(s) != 36 {
		return gocql.UUID{}, false
	}
	u, err := gocql.ParseUUID(s)
	if err != nil {
		return gocql.UUID{}, false
	}
	return u, true
}

// ParseTime parses a version 1 UUID.
func ParseTime(s string) (gocql.UUID, bool) {
	u, ok := Parse(s)
	if !ok || u.Version() != 1 {
		return gocql.UUID{}, false
	}
	return u, true
}

func Bucket(t time.Time) int32 {
	return int32(t.Unix() / int64(BucketSpan/time.Second)) //nolint:gosec // fits int32 for millions of years
}

func BucketOf(id gocql.UUID) int32 {
	return Bucket(id.Time())
}

// After reports whether time UUID a is later than b; anything is later
// than a zero b.
func After(a, b gocql.UUID) bool {
	return a.Timestamp() > b.Timestamp()
}

func IsZero(u gocql.UUID) bool {
	return u == gocql.UUID{}
}
