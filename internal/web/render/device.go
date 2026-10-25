package render

import "strings"

// Device names the browser and system a user agent claims, for people to
// tell their sessions apart.
func Device(ua string) string {
	// The apps say "GoChat/0.2.0 (Android 16; Pixel 8)" or
	// "GoChat/0.2.0 (iOS 18.4; iPhone)".
	if rest, ok := strings.CutPrefix(ua, "GoChat/"); ok {
		_, about, _ := strings.Cut(rest, "(")
		about, _, _ = strings.Cut(about, ")")
		system, model, _ := strings.Cut(about, ";")
		system, _, _ = strings.Cut(strings.TrimSpace(system), " ")
		if model = strings.TrimSpace(model); model != "" && len(model) <= 40 {
			return "GoChat app on " + model
		}
		if system != "" && len(system) <= 20 {
			return "GoChat app on " + system
		}
		return "GoChat app"
	}
	browser := ""
	switch {
	case strings.Contains(ua, "Edg"):
		browser = "Edge"
	case strings.Contains(ua, "OPR/"):
		browser = "Opera"
	case strings.Contains(ua, "Firefox/"), strings.Contains(ua, "FxiOS/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/"), strings.Contains(ua, "CriOS/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}
	system := ""
	switch {
	case strings.Contains(ua, "iPhone"):
		system = "iPhone"
	case strings.Contains(ua, "iPad"):
		system = "iPad"
	case strings.Contains(ua, "Android"):
		system = "Android"
	case strings.Contains(ua, "Windows"):
		system = "Windows"
	case strings.Contains(ua, "CrOS"):
		system = "ChromeOS"
	case strings.Contains(ua, "Macintosh"), strings.Contains(ua, "Mac OS X"):
		system = "macOS"
	case strings.Contains(ua, "Linux"):
		system = "Linux"
	}
	switch {
	case browser != "" && system != "":
		return browser + " on " + system
	case browser != "":
		return browser
	case system != "":
		return system
	}
	if name, _, _ := strings.Cut(ua, "/"); name != "" && len(name) <= 40 && !strings.ContainsAny(name, " ;(") {
		return name
	}
	return "Unknown device"
}
