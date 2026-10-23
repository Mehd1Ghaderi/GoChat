package render

import (
	"io/fs"
	"regexp"
	"testing"

	"github.com/ssenerg/gochat/internal/web/views"
)

func TestTemplatesUseKnownIcons(t *testing.T) {
	re := regexp.MustCompile(`icon "([a-z-]+)"`)
	err := fs.WalkDir(views.Files, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(views.Files, path)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			if _, err := Icon(m[1]); err != nil {
				t.Errorf("%s: %v", path, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
