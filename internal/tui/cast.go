package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/itssoap/cremio/internal/cast"
)

// castMsg marks messages that belong to the cast popup, so the app can route
// them to CastModel regardless of the current screen.
type castMsg interface{ isCastMsg() }

type castDevicesMsg struct{ devices []cast.Device }
type castDiscoveryClosedMsg struct{}
type castStartedMsg struct {
	device  cast.Device
	session cast.Session
}
type castErrMsg struct{ err error }

func (castDevicesMsg) isCastMsg()         {}
func (castDiscoveryClosedMsg) isCastMsg() {}
func (castStartedMsg) isCastMsg()         {}
func (castErrMsg) isCastMsg()             {}

// CastModel is the popup overlay for casting the current stream (or playlist)
// to a device on the LAN. It is compiled into every build but only opened when
// cast.Available is true, so a default build never shows it. The heavy backend
// (Chromecast/DLNA) lives behind the "cast" build tag in the cast package; this
// UI talks only to the cast.Caster interface.
type CastModel struct {
	caster cast.Caster

	visible     bool
	cursor      int
	devices     []cast.Device
	discovering bool
	discoveryCh <-chan []cast.Device
	cancel      context.CancelFunc

	// what to cast, captured when the popup opens
	items      []cast.MediaItem
	startIndex int
	isPlaylist bool

	session cast.Session
	castTo  string
	status  string
	errMsg  string

	width  int
	height int
}

func NewCastModel() CastModel {
	return CastModel{caster: cast.New()}
}

func (m *CastModel) SetSize(w, h int) { m.width = w; m.height = h }

func (m CastModel) IsVisible() bool { return m.visible }

// Open shows the popup, records what should be cast, and starts device
// discovery. items may be empty when the current selection is not castable
// (non HTTP/HTTPS); the popup then explains that and offers no cast action.
func (m *CastModel) Open(items []cast.MediaItem, startIndex int, isPlaylist bool) tea.Cmd {
	m.visible = true
	m.cursor = 0
	m.devices = nil
	m.errMsg = ""
	m.session = nil
	m.castTo = ""
	m.items = items
	m.startIndex = startIndex
	m.isPlaylist = isPlaylist
	switch {
	case len(items) == 0:
		m.status = "Selected stream is not castable (HTTP/HTTPS only)."
	case isPlaylist:
		m.status = fmt.Sprintf("Ready to cast %d items in sequence.", len(items))
	default:
		m.status = "Ready to cast."
	}
	return m.startDiscovery()
}

// Close hides the popup and stops discovery. It does NOT stop an active cast
// session: closing the picker should not interrupt playback on the device.
func (m *CastModel) Close() {
	m.visible = false
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.discovering = false
	m.discoveryCh = nil
}

func (m *CastModel) startDiscovery() tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	ch, err := m.caster.Discover(ctx)
	if err != nil {
		m.errMsg = "discovery failed: " + err.Error()
		cancel()
		return nil
	}
	m.discovering = true
	m.discoveryCh = ch
	return waitForDevices(ch)
}

// waitForDevices reads one device-list update from the discovery channel. On
// each update the caller re-issues it, forming a read loop that ends when the
// backend closes the channel.
func waitForDevices(ch <-chan []cast.Device) tea.Cmd {
	return func() tea.Msg {
		devs, ok := <-ch
		if !ok {
			return castDiscoveryClosedMsg{}
		}
		return castDevicesMsg{devices: devs}
	}
}

func (m *CastModel) castCmd(dev cast.Device) tea.Cmd {
	caster := m.caster
	items := m.items
	start := m.startIndex
	isPlaylist := m.isPlaylist
	return func() tea.Msg {
		ctx := context.Background()
		var (
			sess cast.Session
			err  error
		)
		switch {
		case len(items) == 0:
			return castErrMsg{err: fmt.Errorf("nothing to cast")}
		case isPlaylist && len(items) > 1:
			sess, err = caster.CastQueue(ctx, dev, items, start)
		default:
			sess, err = caster.Cast(ctx, dev, items[0])
		}
		if err != nil {
			return castErrMsg{err: err}
		}
		return castStartedMsg{device: dev, session: sess}
	}
}

func (m *CastModel) sessionCmd(fn func(cast.Session) error) tea.Cmd {
	sess := m.session
	return func() tea.Msg {
		if sess == nil {
			return nil
		}
		if err := fn(sess); err != nil {
			return castErrMsg{err: err}
		}
		return nil
	}
}

func (m CastModel) Update(msg tea.Msg) (CastModel, tea.Cmd) {
	switch msg := msg.(type) {
	case castDevicesMsg:
		m.devices = msg.devices
		if m.cursor >= len(m.devices) && len(m.devices) > 0 {
			m.cursor = len(m.devices) - 1
		}
		if m.discoveryCh != nil {
			return m, waitForDevices(m.discoveryCh) // keep reading updates
		}
		return m, nil

	case castDiscoveryClosedMsg:
		m.discovering = false
		m.discoveryCh = nil
		return m, nil

	case castStartedMsg:
		m.session = msg.session
		m.castTo = msg.device.Name
		m.status = "Casting to " + msg.device.Name
		m.errMsg = ""
		return m, nil

	case castErrMsg:
		m.errMsg = msg.err.Error()
		return m, nil

	case tea.KeyMsg:
		if !m.visible {
			return m, nil
		}
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.devices)-1 {
				m.cursor++
			}
		case "enter":
			if len(m.items) == 0 {
				m.errMsg = "Nothing castable selected."
				return m, nil
			}
			if m.cursor < len(m.devices) {
				return m, m.castCmd(m.devices[m.cursor])
			}
		case " ":
			if m.session != nil {
				return m, m.sessionCmd(func(s cast.Session) error {
					st, _ := s.Status()
					if st.State == "playing" {
						return s.Pause()
					}
					return s.Play()
				})
			}
		case "s":
			if m.session != nil {
				return m, m.sessionCmd(func(s cast.Session) error { return s.Stop() })
			}
		case "n":
			if m.session != nil {
				return m, m.sessionCmd(func(s cast.Session) error { return s.Next() })
			}
		case "p":
			if m.session != nil {
				return m, m.sessionCmd(func(s cast.Session) error { return s.Prev() })
			}
		case "esc", "C":
			m.Close()
		}
	}
	return m, nil
}

func (m CastModel) View() string {
	if !m.visible {
		return ""
	}
	w := m.width - 6
	if w < 40 {
		w = 40
	}

	var b strings.Builder
	b.WriteString(TitleStyle.Render("Cast to device"))
	b.WriteString("\n\n")
	if m.status != "" {
		b.WriteString(SubtitleStyle.Render(m.status))
		b.WriteString("\n\n")
	}

	if len(m.devices) == 0 {
		if m.discovering {
			b.WriteString(HelpStyle.Render("  Searching for devices on your network..."))
		} else {
			b.WriteString(HelpStyle.Render("  No devices found."))
		}
		b.WriteString("\n")
	} else {
		for i, d := range m.devices {
			cursor := "  "
			if i == m.cursor {
				cursor = "▶ "
			}
			name := d.Name
			if name == "" {
				name = d.Addr
			}
			line := fmt.Sprintf("%s%s  (%s)", cursor, name, d.Kind)
			if i == m.cursor {
				b.WriteString(SubtitleStyle.Render(line))
			} else {
				b.WriteString(line)
			}
			b.WriteString("\n")
		}
	}

	if m.castTo != "" {
		b.WriteString("\n" + SubtitleStyle.Render("Now casting to "+m.castTo) + "\n")
	}
	if m.errMsg != "" {
		b.WriteString("\n" + ErrorStyle.Render(m.errMsg) + "\n")
	}

	b.WriteString("\n")
	help := "up/down: device • enter: cast • esc: close"
	if m.session != nil {
		help = "space: play/pause • s: stop • n/p: next/prev • " + help
	}
	b.WriteString(HelpStyle.Render(help))

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(0, 1).
		Width(w)
	return boxStyle.Render(b.String())
}
