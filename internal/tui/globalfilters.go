package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/itssoap/cremio/internal/config"
)

// globalFilterField describes one editable persistent filter.
type globalFilterField struct {
	label string
	help  string
	get   func(*config.GlobalFilters) string
	set   func(*config.GlobalFilters, string)
}

var globalFilterFields = []globalFilterField{
	{
		label: "Addon",
		help:  "match the source/provider tag (e.g. RD, Torrentio, Stremtorz TB)",
		get:   func(g *config.GlobalFilters) string { return g.Addon },
		set:   func(g *config.GlobalFilters, v string) { g.Addon = v },
	},
	{
		label: "File info",
		help:  "match file name / title / description",
		get:   func(g *config.GlobalFilters) string { return g.FileInfo },
		set:   func(g *config.GlobalFilters, v string) { g.FileInfo = v },
	},
	{
		label: "File source",
		help:  "e.g. BluRay, WEB-DL, HDTV",
		get:   func(g *config.GlobalFilters) string { return g.FileSource },
		set:   func(g *config.GlobalFilters, v string) { g.FileSource = v },
	},
	{
		label: "Type",
		help:  "e.g. HTTP, Torrent",
		get:   func(g *config.GlobalFilters) string { return g.Type },
		set:   func(g *config.GlobalFilters, v string) { g.Type = v },
	},
	{
		label: "Release group",
		help:  "e.g. VARYG, Vodes",
		get:   func(g *config.GlobalFilters) string { return g.ReleaseGroup },
		set:   func(g *config.GlobalFilters, v string) { g.ReleaseGroup = v },
	},
}

// GlobalFiltersModel is the tab for editing persistent, cross-addon stream
// filters. Each field uses the same "+include -exclude" keyword syntax as the
// in-view stream filter; empty fields are ignored.
type GlobalFiltersModel struct {
	config  *config.Config
	inputs  []textinput.Model
	cursor  int
	editing bool
	saved   bool
	width   int
	height  int
}

func NewGlobalFiltersModel(cfg *config.Config) GlobalFiltersModel {
	inputs := make([]textinput.Model, len(globalFilterFields))
	for i, f := range globalFilterFields {
		ti := textinput.New()
		ti.CharLimit = 200
		ti.Placeholder = f.help
		ti.SetValue(f.get(&cfg.GlobalFilters))
		inputs[i] = ti
	}
	return GlobalFiltersModel{config: cfg, inputs: inputs}
}

func (m *GlobalFiltersModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	for i := range m.inputs {
		m.inputs[i].Width = w - 20
	}
}

// editingInput reports whether a filter field currently has keyboard focus.
func (m GlobalFiltersModel) editingInput() bool { return m.editing }

func (m *GlobalFiltersModel) commit() {
	for i, f := range globalFilterFields {
		f.set(&m.config.GlobalFilters, strings.TrimSpace(m.inputs[i].Value()))
	}
	_ = m.config.Save()
	m.saved = true
}

func (m GlobalFiltersModel) Update(msg tea.Msg) (GlobalFiltersModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.editing {
			switch msg.String() {
			case "enter":
				m.editing = false
				m.inputs[m.cursor].Blur()
				m.commit()
				return m, nil
			case "esc":
				// Revert this field to the saved value.
				m.editing = false
				m.inputs[m.cursor].Blur()
				m.inputs[m.cursor].SetValue(globalFilterFields[m.cursor].get(&m.config.GlobalFilters))
				return m, nil
			}
			var cmd tea.Cmd
			m.inputs[m.cursor], cmd = m.inputs[m.cursor].Update(msg)
			m.saved = false
			return m, cmd
		}

		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.inputs)-1 {
				m.cursor++
			}
		case "enter":
			m.editing = true
			m.saved = false
			return m, m.inputs[m.cursor].Focus()
		case "c":
			// Clear the selected field.
			m.inputs[m.cursor].SetValue("")
			m.commit()
		case "C":
			// Clear all fields.
			for i := range m.inputs {
				m.inputs[i].SetValue("")
			}
			m.commit()
		}
	}
	return m, nil
}

func (m GlobalFiltersModel) View() string {
	var b strings.Builder
	b.WriteString(TitleStyle.Render("Global Filters"))
	b.WriteString("\n")
	b.WriteString(SubtitleStyle.Render("Applied to every stream cremio shows. Syntax: +include -exclude. Persists across restarts."))
	b.WriteString("\n\n")

	for i, f := range globalFilterFields {
		cursor := "  "
		if i == m.cursor {
			cursor = "▶ "
		}
		label := DetailLabelStyle.Render(padRight(f.label, 14))
		var val string
		if m.editing && i == m.cursor {
			val = m.inputs[i].View()
		} else {
			v := m.inputs[i].Value()
			if v == "" {
				val = SubtitleStyle.Render("(any)")
			} else {
				val = DetailValueStyle.Render(v)
			}
		}
		line := cursor + label + val
		if i == m.cursor && !m.editing {
			b.WriteString(HighlightStyle.Render(cursor+label) + val)
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if m.saved {
		b.WriteString(SubtitleStyle.Render("Saved."))
		b.WriteString("\n")
	}
	if m.editing {
		b.WriteString(HelpStyle.Render("enter: save • esc: cancel edit"))
	} else {
		b.WriteString(HelpStyle.Render("up/down: field • enter: edit • c: clear • C: clear all • tab: switch tab • D: downloads • q: quit"))
	}

	return lipgloss.NewStyle().Padding(0, 0).Render(b.String())
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
