package version

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ssenerg/gochat/internal"
	"github.com/ssenerg/gochat/internal/utils"
)

var ansi = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func Header() {
	lines := []string{
		"",
		entry{key: internal.AppNamePretty()}.encode(utils.ColorBoldGreen),
		entry{key: "Version: ", value: internal.Version()}.encode(utils.ColorBlue),
		"",
	}
	maxLen := utils.Max(visibleStringLength, lines...)
	hyphen := entry{key: strings.Repeat("-", maxLen)}.encode(utils.ColorBlue)
	lines[0], lines[len(lines)-1] = hyphen, hyphen
	for _, line := range lines {
		fmt.Fprintln(os.Stderr, line)
	}
}

type entry struct {
	key   string
	value string
}

func (e entry) encode(color string) string {
	return color + e.key + utils.ColorReset + e.value
}

func visibleStringLength(s string) int {
	return utf8.RuneCountInString(ansi.ReplaceAllString(s, ""))
}
