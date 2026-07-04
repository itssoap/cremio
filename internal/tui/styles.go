package tui

import "github.com/charmbracelet/lipgloss"

// Theme holds all style definitions for a color scheme.
type Theme struct {
	App          lipgloss.Style
	Title        lipgloss.Style
	Subtitle     lipgloss.Style
	Highlight    lipgloss.Style
	Error        lipgloss.Style
	Help         lipgloss.Style
	TabActive    lipgloss.Style
	TabInactive  lipgloss.Style
	DetailLabel  lipgloss.Style
	DetailValue  lipgloss.Style
	InfoPanel    lipgloss.Style
}

// Default theme (current dark look).
var defaultTheme = Theme{
	App: lipgloss.NewStyle().Padding(1, 2),
	Title: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("170")).
		MarginBottom(1),
	Subtitle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")),
	Highlight: lipgloss.NewStyle().
		Foreground(lipgloss.Color("212")),
	Error: lipgloss.NewStyle().
		Foreground(lipgloss.Color("196")),
	Help: lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		MarginTop(1),
	TabActive: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("170")).
		Border(lipgloss.NormalBorder(), false, false, true, false).
		BorderForeground(lipgloss.Color("170")).
		Padding(0, 2),
	TabInactive: lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Padding(0, 2),
	DetailLabel: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("39")),
	DetailValue: lipgloss.NewStyle().
		Foreground(lipgloss.Color("252")),
	InfoPanel: lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("238")).
		Padding(0, 1),
}

// Incognito theme: muted purple/violet tones (Catppuccin Mocha-inspired).
// Distinct enough to immediately signal private mode.
var incognitoTheme = Theme{
	App: lipgloss.NewStyle().Padding(1, 2),
	Title: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#cba6f7")). // mauve
		MarginBottom(1),
	Subtitle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#6c7086")), // overlay0
	Highlight: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#b4befe")), // lavender
	Error: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#f38ba8")), // red
	Help: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#585b70")). // surface2
		MarginTop(1),
	TabActive: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#cba6f7")). // mauve
		Border(lipgloss.NormalBorder(), false, false, true, false).
		BorderForeground(lipgloss.Color("#cba6f7")).
		Padding(0, 2),
	TabInactive: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#6c7086")). // overlay0
		Padding(0, 2),
	DetailLabel: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#89b4fa")), // blue
	DetailValue: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#cdd6f4")), // text
	InfoPanel: lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#45475a")). // surface1
		Padding(0, 1),
}

// Active theme (set during app init).
var activeTheme Theme

// Style aliases used throughout the codebase.
var (
	AppStyle         lipgloss.Style
	TitleStyle       lipgloss.Style
	SubtitleStyle    lipgloss.Style
	HighlightStyle   lipgloss.Style
	ErrorStyle       lipgloss.Style
	HelpStyle        lipgloss.Style
	TabActiveStyle   lipgloss.Style
	TabInactiveStyle lipgloss.Style
	DetailLabelStyle lipgloss.Style
	DetailValueStyle lipgloss.Style
	InfoPanelStyle   lipgloss.Style
)

func init() {
	SetTheme(false)
}

// helpBarHeight returns the number of vertical lines a View reserves for its
// help bar, given the help text and the render width. It accounts for the
// blank separator line between content and help, the help style's top margin,
// and any wrapping of the help text at narrow widths. Keeping this in one place
// ensures SetSize and View can never drift out of sync.
func helpBarHeight(help string, width int) int {
	rendered := HelpStyle.Render(help)
	if width > 0 {
		rendered = HelpStyle.Width(width).Render(help)
	}
	// +1 for the "\n" separator the View inserts before the help bar.
	return lipgloss.Height(rendered) + 1
}

// SetTheme switches all global styles to either default or incognito theme.
func SetTheme(incognito bool) {
	if incognito {
		activeTheme = incognitoTheme
	} else {
		activeTheme = defaultTheme
	}
	AppStyle = activeTheme.App
	TitleStyle = activeTheme.Title
	SubtitleStyle = activeTheme.Subtitle
	HighlightStyle = activeTheme.Highlight
	ErrorStyle = activeTheme.Error
	HelpStyle = activeTheme.Help
	TabActiveStyle = activeTheme.TabActive
	TabInactiveStyle = activeTheme.TabInactive
	DetailLabelStyle = activeTheme.DetailLabel
	DetailValueStyle = activeTheme.DetailValue
	InfoPanelStyle = activeTheme.InfoPanel
}
