package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/itssoap/cremio/internal/config"
	"github.com/itssoap/cremio/internal/stremio"
)

type addonItem struct {
	url      string
	manifest *stremio.Manifest
}

func (a addonItem) Title() string {
	if a.manifest != nil {
		return a.manifest.Name
	}
	return a.url
}
func (a addonItem) Description() string {
	desc := ""
	if a.manifest != nil {
		desc = a.manifest.Description
	} else {
		desc = "Loading..."
	}
	if a.url == config.CinemetaURL {
		return desc + " [required]"
	}
	return desc
}
func (a addonItem) FilterValue() string { return a.url }

type AddonsModel struct {
	list        list.Model
	input       textinput.Model
	client      *stremio.Client
	config      *config.Config
	inputActive bool
	err         error
	warn        string
	saveErr     error
	width       int
	height      int
}

type addonManifestLoadedMsg struct {
	url      string
	manifest *stremio.Manifest
}
type addonValidateErrorMsg struct {
	err error
}

func NewAddonsModel(client *stremio.Client, cfg *config.Config) AddonsModel {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Installed Addons"
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)

	ti := textinput.New()
	ti.Placeholder = "Enter addon URL (e.g. https://v3-cinemeta.strem.io)"
	ti.CharLimit = 500

	m := AddonsModel{
		list:   l,
		input:  ti,
		client: client,
		config: cfg,
	}
	m.refreshList()
	return m
}

func (m AddonsModel) Init() tea.Cmd {
	if len(m.config.Addons) > 0 {
		return m.loadManifests()
	}
	return nil
}

func (m *AddonsModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.input.Width = w - 4
	m.list.SetSize(w, h-6)
}

func (m *AddonsModel) refreshList() {
	items := make([]list.Item, len(m.config.Addons))
	for i, url := range m.config.Addons {
		items[i] = addonItem{url: url}
	}
	m.list.SetItems(items)
}

type addonResult struct {
	url      string
	manifest *stremio.Manifest
}

type addonsRefreshedMsg struct {
	results []addonResult
}

func (m AddonsModel) loadManifests() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		var results []addonResult
		for _, addonURL := range m.config.Addons {
			manifest, err := m.client.FetchManifest(ctx, addonURL)
			if err == nil {
				results = append(results, addonResult{url: addonURL, manifest: manifest})
			}
		}
		return addonsRefreshedMsg{results: results}
	}
}

func (m AddonsModel) validateAndAdd(url string) tea.Cmd {
	return func() tea.Msg {
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			return addonValidateErrorMsg{err: fmt.Errorf("addon URL must start with http:// or https://")}
		}
		ctx := context.Background()
		manifest, err := m.client.FetchManifest(ctx, url)
		if err != nil {
			return addonValidateErrorMsg{err: fmt.Errorf("invalid addon: %w", err)}
		}
		return addonManifestLoadedMsg{url: url, manifest: manifest}
	}
}

func (m AddonsModel) Update(msg tea.Msg) (AddonsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case addonsRefreshedMsg:
		items := make([]list.Item, len(msg.results))
		for i, r := range msg.results {
			items[i] = addonItem{url: r.url, manifest: r.manifest}
		}
		m.list.SetItems(items)
		return m, nil

	case addonManifestLoadedMsg:
		m.config.AddAddon(msg.url)
		if err := m.config.Save(); err != nil {
			m.saveErr = err
		} else {
			m.saveErr = nil
		}
		m.err = nil
		if strings.HasPrefix(msg.url, "http://") {
			m.warn = "Added over plaintext http:// — traffic to this addon is unencrypted."
		} else {
			m.warn = ""
		}
		m.refreshList()
		return m, tea.Batch(m.loadManifests(), func() tea.Msg { return AddonAddedMsg{} })

	case addonValidateErrorMsg:
		m.err = msg.err
		return m, nil

	case tea.KeyMsg:
		if m.inputActive {
			switch msg.String() {
			case "enter":
				url := m.input.Value()
				if url != "" {
					m.inputActive = false
					m.input.Blur()
					m.input.SetValue("")
					return m, m.validateAndAdd(stremio.NormalizeBaseURL(url))
				}
				return m, nil
			case "esc":
				m.inputActive = false
				m.input.Blur()
				m.input.SetValue("")
				return m, nil
			}
		} else {
			switch msg.String() {
			case "a":
				m.inputActive = true
				m.err = nil
				m.warn = ""
				return m, m.input.Focus()
			case "d", "delete":
				if item, ok := m.list.SelectedItem().(addonItem); ok {
					if item.url == config.CinemetaURL {
						m.err = fmt.Errorf("cinemeta is required for title resolution and cannot be removed")
						return m, nil
					}
					m.config.RemoveAddon(item.url)
					if err := m.config.Save(); err != nil {
						m.saveErr = err
					} else {
						m.saveErr = nil
					}
					m.refreshList()
					return m, func() tea.Msg { return AddonRemovedMsg{} }
				}
			}
		}
	}

	var cmd tea.Cmd
	if m.inputActive {
		m.input, cmd = m.input.Update(msg)
	} else {
		m.list, cmd = m.list.Update(msg)
	}
	return m, cmd
}

func (m AddonsModel) View() string {
	var sections []string

	if m.inputActive {
		sections = append(sections, TitleStyle.Render("Add Addon"))
		sections = append(sections, m.input.View())
		if m.err != nil {
			sections = append(sections, ErrorStyle.Render(m.err.Error()))
		}
		sections = append(sections, HelpStyle.Render("enter: validate & add • esc: cancel"))
	} else {
		sections = append(sections, m.list.View())
		if m.err != nil {
			sections = append(sections, ErrorStyle.Render(m.err.Error()))
		}
		if m.warn != "" {
			sections = append(sections, SubtitleStyle.Render("⚠ "+m.warn))
		}
		if m.saveErr != nil {
			sections = append(sections, ErrorStyle.Render(fmt.Sprintf("⚠ Could not save config: %v", m.saveErr)))
		}
		sections = append(sections, HelpStyle.Render("a: add addon • d: remove selected (Cinemeta is locked) • tab: switch tab"))
	}

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}
