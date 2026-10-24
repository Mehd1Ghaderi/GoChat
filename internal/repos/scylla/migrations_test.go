package scylla

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The migration runner skips a statement that starts with a comment, so a
// comment above a statement silently drops it.
func TestMigrationsHaveNoCommentLedStatements(t *testing.T) {
	files, err := filepath.Glob("../../../migrations/scylla/*.cql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, stmt := range strings.Split(string(b), ";") {
			stmt = strings.TrimSpace(stmt)
			if !strings.HasPrefix(stmt, "--") {
				continue
			}
			for _, line := range strings.Split(stmt, "\n") {
				if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "--") {
					t.Errorf("%s: a comment leads %q, which the runner would skip", filepath.Base(f), line)
					break
				}
			}
		}
	}
}

func TestPendingTTLMatchesItsTable(t *testing.T) {
	b, err := os.ReadFile("../../../migrations/scylla/0006_pending_uploads.cql")
	if err != nil {
		t.Fatal(err)
	}
	want := "default_time_to_live = " + strconv.Itoa(int(PendingTTL.Seconds()))
	if !strings.Contains(string(b), want) {
		t.Fatalf("pending_uploads has no %q", want)
	}
}
