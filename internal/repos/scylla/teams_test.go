package scylla

import (
	"bytes"
	"testing"
)

func TestPrefixEnd(t *testing.T) {
	cases := []struct{ in, want []byte }{
		{[]byte{0x01, 0x02}, []byte{0x01, 0x03}},
		{[]byte{0x01, 0xff}, []byte{0x02}},
		{[]byte{0xff, 0xff}, nil},
	}
	for _, c := range cases {
		if got := prefixEnd(c.in); !bytes.Equal(got, c.want) {
			t.Errorf("prefixEnd(%x) = %x, want %x", c.in, got, c.want)
		}
	}
	// Every blob with the prefix sorts before the end.
	p := []byte{0x7f, 0xff}
	for _, b := range [][]byte{{0x7f, 0xff}, {0x7f, 0xff, 0xff, 0xff}} {
		if bytes.Compare(b, prefixEnd(p)) >= 0 {
			t.Errorf("%x is not before the end of %x", b, p)
		}
	}
}
