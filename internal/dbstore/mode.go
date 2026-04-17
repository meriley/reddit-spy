package database

// Digest mode constants shared by the rules + rolling_posts schema and the
// discord/llm packages that dispatch off them. Keeping the strings here
// avoids a cross-package cycle while letting every caller reference a single
// canonical value.
const (
	ModeNarrative = "narrative"
	ModeMusic     = "music"
	ModeSummary   = "summary"
	ModeMedia     = "media"
)

// ValidModes lists every mode the bot knows how to dispatch. Order is stable
// for slash-command choice rendering.
func ValidModes() []string {
	return []string{ModeNarrative, ModeMusic, ModeSummary, ModeMedia}
}

// IsValidMode reports whether m is one of the supported modes.
func IsValidMode(m string) bool {
	for _, v := range ValidModes() {
		if v == m {
			return true
		}
	}
	return false
}
