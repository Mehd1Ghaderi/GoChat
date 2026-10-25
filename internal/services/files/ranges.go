package files

import (
	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	ierrors "github.com/ssenerg/gochat/internal/errors"
)

// Span resolves a byte range against a file of size bytes into the first
// and last byte to read, inclusive. A nil range reads the whole file; ok is
// false when the range starts past the end.
func Span(r *pb.ByteRange, size int64) (first, last int64, ok bool, err error) {
	if r == nil {
		return 0, size - 1, true, nil
	}
	switch {
	case r.Suffix < 0 || r.First < 0:
		return 0, 0, false, ierrors.InvalidArgument("invalid byte range")
	case r.Suffix > 0:
		return max(0, size-r.Suffix), size - 1, size > 0, nil
	case r.First >= size:
		return 0, 0, false, nil
	}
	last = r.Last
	if last < 0 || last >= size {
		last = size - 1
	}
	if last < r.First {
		return 0, 0, false, ierrors.InvalidArgument("invalid byte range")
	}
	return r.First, last, true, nil
}
