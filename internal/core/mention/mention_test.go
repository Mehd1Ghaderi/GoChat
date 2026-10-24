package mention

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in      string
		names   []string
		channel bool
		here    bool
	}{
		{"hi @Bob, see @alice.", []string{"bob", "alice"}, false, false},
		{"mail bob@example.com or @@carol", nil, false, false},
		{"@channel and @here and @all", nil, true, true},
		{"`@nobody` and\n```\n@hidden\n```\n@seen", []string{"seen"}, false, false},
		{"(@dave) @dave @erin-", []string{"dave", "erin"}, false, false},
		{"no mentions here", nil, false, false},
	}
	for _, c := range cases {
		got := Parse(c.in)
		if !slices.Equal(got.Usernames, c.names) || got.Channel != c.channel || got.Here != c.here {
			t.Errorf("%q: got %+v", c.in, got)
		}
	}
	if !Parse("ping @dave").Of("dave") || Parse("ping @david").Of("dave") {
		t.Error("Of")
	}
}

func TestParseBounds(t *testing.T) {
	var text strings.Builder
	for i := range 50 {
		fmt.Fprintf(&text, " @user%c%c", 'a'+i%26, 'a'+i/26)
	}
	if n := len(Parse(text.String()).Usernames); n != MaxNames {
		t.Fatalf("parsed %d names", n)
	}
}
