package tui

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/itssoap/cremio/internal/config"
	"github.com/itssoap/cremio/internal/stremio"
)

type catalogItem struct {
	meta    stremio.MetaPreview
	baseURL string
}

func (i catalogItem) Title() string { return i.meta.Name }
func (i catalogItem) Description() string {
	desc := i.meta.Type
	if i.meta.ReleaseInfo != "" {
		desc += " • " + i.meta.ReleaseInfo
	}
	if i.meta.IMDBRating != "" {
		desc += " • ⭐ " + string(i.meta.IMDBRating)
	}
	return desc
}
func (i catalogItem) FilterValue() string { return i.meta.Name }

type HomeModel struct {
	list    list.Model
	spinner spinner.Model
	client  *stremio.Client
	config  *config.Config
	loading bool
	err     error
	width   int
	height  int
}

type catalogLoadedMsg struct {
	items []catalogItem
}

type catalogErrorMsg struct {
	err error
}

func NewHomeModel(client *stremio.Client, cfg *config.Config) HomeModel {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Catalog"
	l.SetShowHelp(false)
	l.SetFilteringEnabled(true)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("170"))

	return HomeModel{
		list:    l,
		spinner: s,
		client:  client,
		config:  cfg,
		loading: true,
	}
}

func (m HomeModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.loadCatalogs())
}

func (m *HomeModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.list.SetSize(w, h)
}

func (m HomeModel) loadCatalogs() tea.Cmd {
	// Snapshot the addon list so the goroutine doesn't race with the main
	// loop mutating config.Addons (e.g. on addon add/remove).
	addons := append([]string(nil), m.config.Addons...)
	return func() tea.Msg {
		var allItems []catalogItem
		ctx := context.Background()

		for _, addonURL := range addons {
			manifest, err := m.client.FetchManifest(ctx, addonURL)
			if err != nil {
				continue
			}

			for _, cat := range manifest.Catalogs {
				// Skip search-only catalogs
				searchOnly := false
				for _, e := range cat.Extra {
					if e.Name == "search" && e.IsRequired {
						searchOnly = true
						break
					}
				}
				if searchOnly {
					continue
				}

				resp, err := m.client.FetchCatalog(ctx, addonURL, cat.Type, cat.ID)
				if err != nil {
					continue
				}
				for _, meta := range resp.Metas {
					allItems = append(allItems, catalogItem{meta: meta, baseURL: addonURL})
				}
			}
		}
		if len(allItems) == 0 && len(addons) > 0 {
			return catalogErrorMsg{err: fmt.Errorf("no catalog items loaded — check that your addons are reachable")}
		}
		return catalogLoadedMsg{items: allItems}
	}
}

// ── Update ──────────────────────────────────────────────────────────────────

func (m HomeModel) Update(msg tea.Msg) (HomeModel, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if m.loading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case catalogErrorMsg:
		m.loading = false
		m.err = msg.err
		return m, nil

	case catalogLoadedMsg:
		m.loading = false
		items := make([]list.Item, len(msg.items))
		for i, item := range msg.items {
			items[i] = item
		}
		m.list.SetItems(items)
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "enter" {
			if item, ok := m.list.SelectedItem().(catalogItem); ok {
				return m, func() tea.Msg {
					return NavigateToDetailMsg{
						ID:      item.meta.ID,
						Type:    item.meta.Type,
						BaseURL: item.baseURL,
					}
				}
			}
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m HomeModel) View() string {
	if m.loading {
		return m.spinner.View() + " Loading catalogs..."
	}
	if m.err != nil {
		return ErrorStyle.Render(fmt.Sprintf("Error: %v", m.err))
	}
	if len(m.config.Addons) == 0 {
		return SubtitleStyle.Render("No addons installed. Press Tab to go to Addons and add one.")
	}
	return m.list.View()
}
