package emoji

import "testing"

func TestNames(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range All {
		if seen[e.Name] || e.Char == "" || !Valid(e.Name) {
			t.Errorf("bad entry %+v", e)
		}
		seen[e.Name] = true
	}
	if c, ok := Char("+1"); !ok || c != "👍" || Valid("+1") || Valid("<b>") {
		t.Error("aliases or validity")
	}
}
