package ids

import (
	"testing"
	"time"
)

func TestNewTimeIsMonotonic(t *testing.T) {
	prev := NewTime()
	for range 10000 {
		next := NewTime()
		if next.Timestamp() <= prev.Timestamp() {
			t.Fatal("time uuids went backwards")
		}
		if next.Version() != 1 || next[10]&1 != 1 {
			t.Fatal("not a version 1 uuid with a random node")
		}
		prev = next
	}
}

func TestParse(t *testing.T) {
	id := NewTime()
	if got, ok := ParseTime(id.String()); !ok || got != id {
		t.Fatal("round trip")
	}
	if _, ok := ParseTime(New().String()); ok {
		t.Fatal("random uuid parsed as a time uuid")
	}
	if _, ok := Parse("not-a-uuid"); ok {
		t.Fatal("garbage parsed")
	}
}

func TestBucket(t *testing.T) {
	now := time.Now()
	id := NewTime()
	if BucketOf(id) != Bucket(now) && BucketOf(id) != Bucket(now.Add(time.Second)) {
		t.Fatal("bucket of a fresh id")
	}
	if Bucket(now.Add(-BucketSpan)) != Bucket(now)-1 {
		t.Fatal("adjacent buckets")
	}
}
