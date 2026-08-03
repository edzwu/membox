package tui

import "github.com/charmbracelet/lipgloss"

// The palette follows pi's visual hierarchy: quiet editor borders, a cyan
// focused border, muted secondary text, and a restrained teal accent.
var colors = struct {
	Accent, Border, BorderAccent, BorderMuted     lipgloss.AdaptiveColor
	Text, Muted, Dim, SelectedBG                  lipgloss.AdaptiveColor
	Success, Error, Warning, Heading, HighlightBG lipgloss.AdaptiveColor
}{
	Accent:       lipgloss.AdaptiveColor{Dark: "#8abeb7", Light: "#356b68"},
	Border:       lipgloss.AdaptiveColor{Dark: "#5f87ff", Light: "#3159b8"},
	BorderAccent: lipgloss.AdaptiveColor{Dark: "#00d7ff", Light: "#007c99"},
	BorderMuted:  lipgloss.AdaptiveColor{Dark: "#505050", Light: "#b0b0b0"},
	Text:         lipgloss.AdaptiveColor{Dark: "#d4d4d4", Light: "#242424"},
	Muted:        lipgloss.AdaptiveColor{Dark: "#808080", Light: "#666666"},
	Dim:          lipgloss.AdaptiveColor{Dark: "#666666", Light: "#888888"},
	SelectedBG:   lipgloss.AdaptiveColor{Dark: "#3a3a4a", Light: "#e4e8f2"},
	Success:      lipgloss.AdaptiveColor{Dark: "#b5bd68", Light: "#4f6f24"},
	Error:        lipgloss.AdaptiveColor{Dark: "#cc6666", Light: "#a03030"},
	Warning:      lipgloss.AdaptiveColor{Dark: "#ffff00", Light: "#8a6500"},
	Heading:      lipgloss.AdaptiveColor{Dark: "#f0c674", Light: "#8a5a00"},
	HighlightBG:  lipgloss.AdaptiveColor{Dark: "#d99a2b", Light: "#ffd98a"},
}

var (
	accentStyle  = lipgloss.NewStyle().Foreground(colors.Accent)
	mutedStyle   = lipgloss.NewStyle().Foreground(colors.Muted)
	dimStyle     = lipgloss.NewStyle().Foreground(colors.Dim)
	previewStyle = lipgloss.NewStyle().Foreground(colors.Text)
	errorStyle   = lipgloss.NewStyle().Foreground(colors.Error)
	headingStyle = lipgloss.NewStyle().Foreground(colors.Heading).Bold(true)
)
