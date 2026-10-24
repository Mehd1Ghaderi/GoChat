// Package authztest builds an authorizer holding the policy the scylla
// migrations insert, for tests.
package authztest

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	stringadapter "github.com/casbin/casbin/v2/persist/string-adapter"
	"go.uber.org/zap"

	"github.com/ssenerg/gochat/internal/authz"
)

var ruleRe = regexp.MustCompile(`INSERT INTO casbin_rules \(ptype, v0, v1, v2, v3, v4, v5\) VALUES \(([^)]*)\)`)

// Policy is the migrations' casbin rules as CSV lines.
func Policy(t testing.TB) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	dir := os.DirFS(filepath.Join(filepath.Dir(file), "../../../migrations/scylla"))
	files, err := fs.Glob(dir, "*.cql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found: %v", err)
	}
	var lines []string
	for _, f := range files {
		b, err := fs.ReadFile(dir, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ruleRe.FindAllStringSubmatch(string(b), -1) {
			var vals []string
			for _, v := range strings.Split(m[1], ",") {
				if v = strings.Trim(strings.TrimSpace(v), "'"); v != "" {
					vals = append(vals, v)
				}
			}
			lines = append(lines, strings.Join(vals, ", "))
		}
	}
	return strings.Join(lines, "\n")
}

func New(t testing.TB) *authz.Authorizer {
	t.Helper()
	az, err := authz.New(zap.NewNop(), stringadapter.NewAdapter(Policy(t)))
	if err != nil {
		t.Fatal(err)
	}
	return az
}
