package files

import (
	"testing"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
)

func TestSpan(t *testing.T) {
	cases := []struct {
		r           *pb.ByteRange
		first, last int64
		ok, invalid bool
	}{
		{nil, 0, 99, true, false},
		{&pb.ByteRange{First: 0, Last: 1}, 0, 1, true, false},
		{&pb.ByteRange{First: 10, Last: -1}, 10, 99, true, false},
		{&pb.ByteRange{First: 90, Last: 500}, 90, 99, true, false},
		{&pb.ByteRange{Suffix: 30}, 70, 99, true, false},
		{&pb.ByteRange{Suffix: 500}, 0, 99, true, false},
		{&pb.ByteRange{First: 100, Last: -1}, 0, 0, false, false},
		{&pb.ByteRange{First: 5, Last: 4}, 0, 0, false, true},
		{&pb.ByteRange{First: -1, Last: 4}, 0, 0, false, true},
		{&pb.ByteRange{Suffix: -3}, 0, 0, false, true},
	}
	for _, c := range cases {
		first, last, ok, err := Span(c.r, 100)
		if (err != nil) != c.invalid || ok != c.ok || (ok && (first != c.first || last != c.last)) {
			t.Errorf("Span(%v, 100) = %d, %d, %v, %v", c.r, first, last, ok, err)
		}
	}
}
