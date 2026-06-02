package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/itssoap/cremio/internal/config"
	"github.com/itssoap/cremio/internal/stremio"
)

// searchAddonItem is a selectable entry in the addon selector popup.
type searchAddonItem struct {
	url  string
	name string
}

// searchAddonNameMsg carries the resolved display name of the active search addon.
type searchAddonNameMsg struct{ name string }

// searchAddonListMsg carries the full list of addons for the selector popup.
type searchAddonListMsg struct{ items []searchAddonItem }

type SearchModel struct {
	input        textinput.Model
	results      list.Model
	spinner      spinner.Model
	client       *stremio.Client
	config       *config.Config
	inputFocused bool
	searching    bool
	err          error
	width        int
	height       int

	// active search addon display name
	addonName string

	// addon selector popup
	selectorActive  bool
	selectorLoading bool
	selectorItems   []searchAddonItem
	selectorCursor  int
}

type searchResultsMsg struct {
	items []catalogItem
}
type searchErrorMsg struct {
	err error
}

func NewSearchModel(client *stremio.Client, cfg *config.Config) SearchModel {
	ti := textinput.New()
	ti.Placeholder = "Search movies & series..."
	ti.CharLimit = 100

	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Results"
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("170"))

	return SearchModel{
		input:   ti,
		results: l,
		spinner: s,
		client:  client,
		config:  cfg,
	}
}

func (m SearchModel) Init() tea.Cmd {
	return m.loadAddonName()
}

func (m *SearchModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.input.Width = w - 4
	m.results.SetSize(w, h-7) // account for input (2 lines), help (1 line), spacing
}

// resolveSearchAddon returns the addon URL to search with, falling back to
// the first addon in the list if SearchAddon is no longer installed.
func (m SearchModel) resolveSearchAddon() string {
	for _, a := range m.config.Addons {
		if a == m.config.SearchAddon {
			return a
		}
	}
	if len(m.config.Addons) > 0 {
		return m.config.Addons[0]
	}
	return ""
}

// loadAddonName fetches the manifest of the active search addon to get its display name.
func (m SearchModel) loadAddonName() tea.Cmd {
	addonURL := m.resolveSearchAddon()
	if addonURL == "" {
		return nil
	}
	return func() tea.Msg {
		ctx := context.Background()
		manifest, err := m.client.FetchManifest(ctx, addonURL)
		if err != nil || manifest.Name == "" {
			return searchAddonNameMsg{name: addonURL}
		}
		return searchAddonNameMsg{name: manifest.Name}
	}
}

// loadAddonNames fetches manifests for all installed addons (for the selector popup).
func (m SearchModel) loadAddonNames() tea.Cmd {
	addons := append([]string(nil), m.config.Addons...)
	return func() tea.Msg {
		ctx := context.Background()
		items := make([]searchAddonItem, len(addons))
		for i, url := range addons {
			name := url
			manifest, err := m.client.FetchManifest(ctx, url)
			if err == nil && manifest.Name != "" {
				name = manifest.Name
			}
			items[i] = searchAddonItem{url: url, name: name}
		}
		return searchAddonListMsg{items: items}
	}
}

func (m SearchModel) search(query string) tea.Cmd {
	addonURL := m.resolveSearchAddon()
	return func() tea.Msg {
		if addonURL == "" {
			return searchErrorMsg{err: fmt.Errorf("no addons installed")}
		}

		ctx := context.Background()
		queryLower := strings.ToLower(query)
		var allItems []catalogItem

		manifest, err := m.client.FetchManifest(ctx, addonURL)
		if err != nil {
			return searchErrorMsg{err: fmt.Errorf("could not reach search addon: %w", err)}
		}

		for _, cat := range manifest.Catalogs {
			if cat.SupportsSearch() {
				resp, err := m.client.SearchCatalog(ctx, addonURL, cat.Type, cat.ID, query)
				if err != nil {
					continue
				}
				for _, meta := range resp.Metas {
					allItems = append(allItems, catalogItem{meta: meta, baseURL: addonURL})
				}
			} else {
				hasRequired := false
				for _, e := range cat.Extra {
					if e.IsRequired {
						hasRequired = true
						break
					}
				}
				if hasRequired {
					continue
				}
				resp, err := m.client.FetchCatalog(ctx, addonURL, cat.Type, cat.ID)
				if err != nil {
					continue
				}
				for _, meta := range resp.Metas {
					if strings.Contains(strings.ToLower(meta.Name), queryLower) {
						allItems = append(allItems, catalogItem{meta: meta, baseURL: addonURL})
					}
				}
			}
		}

		if len(allItems) == 0 {
			return searchErrorMsg{err: fmt.Errorf("no results found")}
		}
		return searchResultsMsg{items: allItems}
	}
}

func (m SearchModel) Update(msg tea.Msg) (SearchModel, tea.Cmd) {
	switch msg := msg.(type) {
	case searchAddonNameMsg:
		m.addonName = msg.name
		return m, nil

	case searchAddonListMsg:
		m.selectorItems = msg.items
		m.selectorLoading = false
		// Position cursor on current selection
		for i, item := range m.selectorItems {
			if item.url == m.config.SearchAddon {
				m.selectorCursor = i
				break
			}
		}
		return m, nil

	case searchResultsMsg:
		m.searching = false
		items := make([]list.Item, len(msg.items))
		for i, item := range msg.items {
			items[i] = item
		}
		m.results.SetItems(items)
		return m, nil

	case searchErrorMsg:
		m.searching = false
		m.err = msg.err
		return m, nil

	case spinner.TickMsg:
		if m.searching {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyMsg:
		// Selector popup keys
		if m.selectorActive {
			switch msg.String() {
			case "up", "k":
				if m.selectorCursor > 0 {
					m.selectorCursor--
				}
			case "down", "j":
				if m.selectorCursor < len(m.selectorItems)-1 {
					m.selectorCursor++
				}
			case "enter":
				if !m.selectorLoading && len(m.selectorItems) > 0 {
					selected := m.selectorItems[m.selectorCursor]
					m.config.SearchAddon = selected.url
					_ = m.config.Save()
					m.addonName = ""
					m.selectorActive = false
					return m, m.loadAddonName()
				}
			case "esc":
				m.selectorActive = false
			}
			return m, nil
		}

		if m.inputFocused {
			switch msg.String() {
			case "enter":
				query := m.input.Value()
				if query != "" {
					m.inputFocused = false
					m.input.Blur()
					m.searching = true
					m.err = nil
					return m, tea.Batch(m.spinner.Tick, m.search(query))
				}
				return m, nil
			case "esc":
				m.inputFocused = false
				m.input.Blur()
				return m, nil
			}
		} else {
			switch msg.String() {
			case "/":
				m.inputFocused = true
				return m, m.input.Focus()
			case "s":
				m.selectorActive = true
				m.selectorLoading = true
				m.selectorItems = nil
				return m, m.loadAddonNames()
			case "enter":
				if item, ok := m.results.SelectedItem().(catalogItem); ok {
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
	}

	var cmd tea.Cmd
	if m.inputFocused {
		m.input, cmd = m.input.Update(msg)
	} else if !m.selectorActive {
		m.results, cmd = m.results.Update(msg)
	}
	return m, cmd
}

func (m SearchModel) View() string {
	// Addon selector popup
	if m.selectorActive {
		var b strings.Builder
		b.WriteString(TitleStyle.Render("Select search addon") + "\n\n")
		if m.selectorLoading {
			b.WriteString(SubtitleStyle.Render("Loading addons...") + "\n")
		} else if len(m.selectorItems) == 0 {
			b.WriteString(SubtitleStyle.Render("No addons installed.") + "\n")
		} else {
			for i, item := range m.selectorItems {
				cursor := "  "
				if i == m.selectorCursor {
					cursor = "> "
				}
				line := cursor + item.name
				if item.url == m.config.SearchAddon {
					line += HelpStyle.Render("  [active]")
				}
				if i == m.selectorCursor {
					b.WriteString(HighlightStyle.Render(line) + "\n")
				} else {
					b.WriteString(line + "\n")
				}
			}
		}
		b.WriteString("\n" + HelpStyle.Render("up/down: navigate • enter: select • esc: cancel"))
		return InfoPanelStyle.Render(b.String())
	}

	var sections []string
	sections = append(sections, m.input.View())

	if m.searching {
		sections = append(sections, "\n"+m.spinner.View()+" Searching...")
	} else if m.err != nil {
		sections = append(sections, "\n"+ErrorStyle.Render(m.err.Error()))
	} else {
		sections = append(sections, m.results.View())
	}

	via := "..."
	if m.addonName != "" {
		via = m.addonName
	}
	help := HelpStyle.Render(fmt.Sprintf("/ focus search • enter submit • esc unfocus • s: search addon  via: %s", via))
	sections = append(sections, help)

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}
