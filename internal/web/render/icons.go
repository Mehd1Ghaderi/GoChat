package render

import (
	"fmt"
	"html/template"
)

// icons are the interface's line icons: drawn on a 24px grid, stroked with
// the current text color so they follow every theme.
var icons = map[string]string{
	"menu":     `<path d="M4 7h16M4 12h16M4 17h16"/>`,
	"close":    `<path d="M6 6l12 12M18 6L6 18"/>`,
	"plus":     `<path d="M12 5v14M5 12h14"/>`,
	"search":   `<circle cx="11" cy="11" r="6.5"/><path d="M16 16l4 4"/>`,
	"more":     `<circle cx="6" cy="12" r="1.4" fill="currentColor" stroke="none"/><circle cx="12" cy="12" r="1.4" fill="currentColor" stroke="none"/><circle cx="18" cy="12" r="1.4" fill="currentColor" stroke="none"/>`,
	"settings": `<path d="M4 6.5h8.5M16.5 6.5H20M4 12h3.5M11.5 12H20M4 17.5h10.5M18.5 17.5H20"/><circle cx="14.5" cy="6.5" r="2"/><circle cx="9.5" cy="12" r="2"/><circle cx="16.5" cy="17.5" r="2"/>`,
	"sign-out": `<path d="M10 4H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h3M15 8l4 4-4 4M19 12H10"/>`,
	"back":     `<path d="M19 12H5M11 18l-6-6 6-6"/>`,
	"hash":     `<path d="M5 9h14M5 15h14M10.5 4L8.5 20M15.5 4l-2 16"/>`,
	"lock":     `<rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8.5 11V8a3.5 3.5 0 0 1 7 0v3"/>`,
	"members":  `<circle cx="9" cy="8.5" r="3.5"/><path d="M3 20a6 6 0 0 1 12 0M15.5 5.2a3.5 3.5 0 0 1 0 6.6M17.5 14.4A6 6 0 0 1 21 20"/>`,
	"invite":   `<circle cx="9" cy="8.5" r="3.5"/><path d="M3 20a6 6 0 0 1 12 0M19 8v6M16 11h6"/>`,
	"leave":    `<path d="M14 4h3a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-3M9 8l-4 4 4 4M5 12h10"/>`,
	"attach":   `<path d="M20 11.5l-7.6 7.6a5 5 0 0 1-7.1-7.1l7.9-7.9a3.4 3.4 0 0 1 4.8 4.8l-7.9 7.9a1.7 1.7 0 0 1-2.4-2.4l7.2-7.2"/>`,
	"send":     `<path d="M5 12L3.5 4.5 21 12 3.5 19.5 5 12zM5 12h7"/>`,
	"edit":     `<path d="M4 20h4L19 9a2.8 2.8 0 0 0-4-4L4 16v4zM13.5 6.5l4 4"/>`,
	"trash":    `<path d="M4 7h16M9.5 7V4.5h5V7M6.5 7l.8 11.2A2 2 0 0 0 9.3 20h5.4a2 2 0 0 0 2-1.8L17.5 7M10 11v5M14 11v5"/>`,
	"download": `<path d="M12 4v11M7.5 10.5L12 15l4.5-4.5M5 20h14"/>`,
	"photo":    `<rect x="3.5" y="5" width="17" height="14" rx="2.5"/><circle cx="9" cy="10" r="1.75"/><path d="M20.5 15.5l-4.5-4.5-8.5 8"/>`,
	"check":    `<path d="M5 12.5l4.5 4.5L19 7.5"/>`,
	"browse":   `<path d="M4 6h16M4 12h10M4 18h7"/><circle cx="17.5" cy="16.5" r="2.5"/><path d="M19.5 18.5L21 20"/>`,
}

// Icon renders a named icon for a template. Templates only name known icons,
// which a test checks.
func Icon(name string) (template.HTML, error) {
	body, ok := icons[name]
	if !ok {
		return "", fmt.Errorf("unknown icon %q", name)
	}
	return template.HTML(`<svg class="i" viewBox="0 0 24 24" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`), nil //nolint:gosec // fixed markup
}
