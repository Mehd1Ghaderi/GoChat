// Package emoji names the emoji people react with and write as :name:.
// Only these are stored, so a reaction is always one of them.
package emoji

type Emoji struct {
	Name string
	Char string
}

// All lists the emoji in the order the picker shows them.
var All = []Emoji{
	{"thumbsup", "👍"}, {"thumbsdown", "👎"}, {"heart", "❤️"}, {"joy", "😂"}, {"tada", "🎉"}, {"eyes", "👀"},
	{"white_check_mark", "✅"}, {"pray", "🙏"}, {"fire", "🔥"}, {"rocket", "🚀"}, {"clap", "👏"}, {"raised_hands", "🙌"},
	{"smile", "😄"}, {"laughing", "😆"}, {"rofl", "🤣"}, {"slightly_smiling_face", "🙂"}, {"wink", "😉"}, {"blush", "😊"},
	{"heart_eyes", "😍"}, {"sunglasses", "😎"}, {"upside_down", "🙃"}, {"sweat_smile", "😅"}, {"relieved", "😌"}, {"nerd", "🤓"},
	{"thinking", "🤔"}, {"neutral_face", "😐"}, {"confused", "😕"}, {"open_mouth", "😮"}, {"scream", "😱"}, {"cry", "😢"},
	{"sob", "😭"}, {"angry", "😠"}, {"face_palm", "🤦"}, {"shrug", "🤷"}, {"sleepy", "😪"}, {"skull", "💀"},
	{"muscle", "💪"}, {"ok_hand", "👌"}, {"wave", "👋"}, {"point_up", "☝️"}, {"handshake", "🤝"}, {"crossed_fingers", "🤞"},
	{"100", "💯"}, {"sparkles", "✨"}, {"star", "⭐"}, {"zap", "⚡"}, {"bulb", "💡"}, {"dart", "🎯"},
	{"trophy", "🏆"}, {"gift", "🎁"}, {"cake", "🎂"}, {"coffee", "☕"}, {"pizza", "🍕"}, {"beers", "🍻"},
	{"memo", "📝"}, {"bug", "🐛"}, {"warning", "⚠️"}, {"x", "❌"}, {"question", "❓"}, {"exclamation", "❗"},
	{"rainbow", "🌈"}, {"sunny", "☀️"}, {"crescent_moon", "🌙"}, {"robot", "🤖"}, {"ghost", "👻"}, {"see_no_evil", "🙈"},
	{"speech_balloon", "💬"}, {"calendar", "📅"}, {"headphones", "🎧"}, {"house", "🏠"}, {"palm_tree", "🌴"}, {"face_with_thermometer", "🤒"},
}

var byName = func() map[string]string {
	m := make(map[string]string, len(All)+2)
	for _, e := range All {
		m[e.Name] = e.Char
	}
	m["+1"], m["-1"] = m["thumbsup"], m["thumbsdown"]
	return m
}()

// Char returns the emoji of name, false for an unknown name.
func Char(name string) (string, bool) {
	c, ok := byName[name]
	return c, ok
}

// Valid reports whether name is an emoji people can react with.
func Valid(name string) bool {
	_, ok := byName[name]
	return ok && name != "+1" && name != "-1"
}
