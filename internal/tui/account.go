package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/itssoap/cremio/internal/account"
	"github.com/itssoap/cremio/internal/config"
)

// accountState is the Account tab's top-level mode.
type accountState int

const (
	accLoggedOut accountState = iota
	accSubmitting
	accLoggedIn
)

// Messages the app routes to drive login/logout/sync.
type accountLoginMsg struct {
	user     account.User
	authKey  string
	validate bool // true when produced by ValidateCmd (startup session check)
	err      error
}
type accountLogoutMsg struct{}
type accountSyncRequestMsg struct{}

// AccountModel is the Account tab: log in with an existing Stremio account (or a
// pasted session key) and manage addon/history sync. The password is never
// rendered or stored; only the session token is persisted (in the account pkg).
type AccountModel struct {
	config    *config.Config
	client    *account.Client
	incognito bool

	state   accountState
	inputs  []textinput.Model // 0 email, 1 password, 2 authKey
	focus   int
	focused bool

	user     account.User
	errMsg   string
	status   string
	lastSync time.Time

	width  int
	height int
}

const (
	accEmailIdx = 0
	accPassIdx  = 1
	accKeyIdx   = 2
)

func NewAccountModel(cfg *config.Config, incognito bool) AccountModel {
	client := account.New()

	email := textinput.New()
	email.Placeholder = "you@example.com"
	email.CharLimit = 200

	pass := textinput.New()
	pass.Placeholder = "password"
	pass.EchoMode = textinput.EchoPassword
	pass.CharLimit = 200

	key := textinput.New()
	key.Placeholder = "or paste an existing Stremio authKey"
	key.EchoMode = textinput.EchoPassword
	key.CharLimit = 400

	m := AccountModel{
		config:    cfg,
		client:    client,
		incognito: incognito,
		inputs:    []textinput.Model{email, pass, key},
		state:     accLoggedOut,
	}

	// Account activity is fully disabled in incognito: no session load, no
	// login, no sync. Locally-added addons are untouched.
	if incognito {
		return m
	}

	// Reuse a persisted session if present; validity is confirmed by the app
	// calling ValidateCmd on startup.
	if saved, _ := account.LoadAuthKey(); saved != "" {
		client.SetAuthKey(saved)
		m.state = accLoggedIn
		m.user = account.User{Email: cfg.Account.Email}
	} else {
		m.focused = true
		m.inputs[accEmailIdx].Focus()
	}
	return m
}

func (m *AccountModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	for i := range m.inputs {
		m.inputs[i].Width = max(20, w-24)
	}
}

// Client exposes the underlying API client so the app can run sync commands.
func (m AccountModel) Client() *account.Client { return m.client }

// LoggedIn reports whether a session is active.
func (m AccountModel) LoggedIn() bool { return m.state == accLoggedIn }

// editingInput reports whether the login form is capturing keystrokes, so the
// app suppresses global keys (q/tab) while the user types.
func (m AccountModel) editingInput() bool {
	return !m.incognito && m.state == accLoggedOut && m.focused
}

// SetLastSync records the last successful sync time (called by the app).
func (m *AccountModel) SetLastSync(t time.Time) { m.lastSync = t }

// SetStatus sets a one-line status shown under the account details.
func (m *AccountModel) SetStatus(s string) { m.status = s }

// ValidateCmd checks a persisted key by fetching the user; on failure the app
// receives an accountLoginMsg with an error and can flip back to logged-out.
func (m AccountModel) ValidateCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		u, err := client.GetUser(ctx)
		return accountLoginMsg{user: u, authKey: client.AuthKey(), validate: true, err: err}
	}
}

// loginCmd logs in (or validates a pasted key) off the UI thread and persists
// the resulting token on success.
func (m AccountModel) loginCmd(email, password, pastedKey string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		var (
			u   account.User
			err error
		)
		if strings.TrimSpace(pastedKey) != "" {
			client.SetAuthKey(strings.TrimSpace(pastedKey))
			u, err = client.GetUser(ctx)
		} else {
			u, err = client.Login(ctx, strings.TrimSpace(email), password)
		}
		if err != nil {
			client.SetAuthKey("")
			return accountLoginMsg{err: err}
		}
		if err := account.SaveAuthKey(client.AuthKey()); err != nil {
			return accountLoginMsg{err: err}
		}
		return accountLoginMsg{user: u, authKey: client.AuthKey()}
	}
}

func (m AccountModel) logoutCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.Logout(ctx)
		_ = account.DeleteAuthKey()
		return accountLogoutMsg{}
	}
}

// ApplyLogin folds a login result into the model (called by the app so it can
// also trigger a sync). Returns true on success.
func (m *AccountModel) ApplyLogin(msg accountLoginMsg) bool {
	if msg.err != nil {
		// A transient network failure while validating a saved session must NOT
		// log the user out: keep the session and run offline against local data.
		if msg.validate && isTransientErr(msg.err) {
			m.state = accLoggedIn
			m.user = account.User{Email: m.config.Account.Email}
			m.status = "Offline: could not reach Stremio, using local data."
			m.errMsg = ""
			return false
		}
		// Genuine auth rejection (or interactive login failure): drop the session
		// and clear the stored key so it is not re-tried every startup.
		m.client.SetAuthKey("")
		_ = account.DeleteAuthKey()
		m.state = accLoggedOut
		m.focused = true
		m.errMsg = friendlyAuthError(msg.err)
		m.inputs[accPassIdx].SetValue("")
		m.inputs[accEmailIdx].Focus()
		m.focus = accEmailIdx
		return false
	}
	m.state = accLoggedIn
	m.user = msg.user
	m.errMsg = ""
	m.config.Account.Enabled = true
	m.config.Account.Email = msg.user.Email
	if !m.config.Account.SyncAddons && !m.config.Account.SyncHistory {
		m.config.Account.SyncAddons = true
		m.config.Account.SyncHistory = true
	}
	// Clear sensitive form fields.
	for i := range m.inputs {
		m.inputs[i].SetValue("")
		m.inputs[i].Blur()
	}
	_ = m.config.Save()
	return true
}

// ApplyLogout resets the model to logged-out state.
func (m *AccountModel) ApplyLogout() {
	m.state = accLoggedOut
	m.user = account.User{}
	m.status = ""
	m.errMsg = ""
	m.focused = true
	m.focus = accEmailIdx
	m.inputs[accEmailIdx].Focus()
	m.config.Account.Enabled = false
	m.config.Account.Email = ""
	_ = m.config.Save()
}

func (m *AccountModel) blurAll() {
	m.focused = false
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
}

func (m *AccountModel) focusField(i int) {
	m.focused = true
	m.focus = i
	for j := range m.inputs {
		if j == i {
			m.inputs[j].Focus()
		} else {
			m.inputs[j].Blur()
		}
	}
}

func (m AccountModel) Update(msg tea.Msg) (AccountModel, tea.Cmd) {
	if m.incognito {
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch m.state {
	case accLoggedIn:
		switch key.String() {
		case "s":
			m.status = "Syncing..."
			return m, func() tea.Msg { return accountSyncRequestMsg{} }
		case "L":
			m.status = "Logging out..."
			return m, m.logoutCmd()
		}
		return m, nil

	case accSubmitting:
		return m, nil

	default: // accLoggedOut
		if m.focused {
			switch key.String() {
			case "esc":
				m.blurAll()
				return m, nil
			case "tab", "down":
				m.focusField((m.focus + 1) % len(m.inputs))
				return m, nil
			case "shift+tab", "up":
				m.focusField((m.focus - 1 + len(m.inputs)) % len(m.inputs))
				return m, nil
			case "enter":
				email := m.inputs[accEmailIdx].Value()
				pass := m.inputs[accPassIdx].Value()
				pasted := m.inputs[accKeyIdx].Value()
				if strings.TrimSpace(email) == "" && strings.TrimSpace(pasted) == "" {
					m.errMsg = "Enter your email + password, or paste an authKey."
					return m, nil
				}
				m.state = accSubmitting
				m.errMsg = ""
				return m, m.loginCmd(email, pass, pasted)
			}
			var cmd tea.Cmd
			m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
			return m, cmd
		}
		// Not focused: enter re-focuses the form.
		if key.String() == "enter" {
			m.focusField(accEmailIdx)
		}
		return m, nil
	}
}

func (m AccountModel) View() string {
	var b strings.Builder
	b.WriteString(TitleStyle.Render("Account"))
	b.WriteString("\n")

	if m.incognito {
		b.WriteString(SubtitleStyle.Render("Account features are disabled in incognito mode."))
		b.WriteString("\n")
		b.WriteString(SubtitleStyle.Render("No login, no sync, no account activity. Your addons are untouched."))
		return lipgloss.NewStyle().Render(b.String())
	}

	switch m.state {
	case accLoggedIn:
		b.WriteString(SubtitleStyle.Render("Signed in to your Stremio account."))
		b.WriteString("\n\n")
		email := m.user.Email
		if email == "" {
			email = "(unknown)"
		}
		b.WriteString(DetailLabelStyle.Render(padRight("Account", 14)) + DetailValueStyle.Render(email) + "\n")
		b.WriteString(DetailLabelStyle.Render(padRight("Sync addons", 14)) + DetailValueStyle.Render(onOff(m.config.Account.SyncAddons)) + "\n")
		b.WriteString(DetailLabelStyle.Render(padRight("Sync history", 14)) + DetailValueStyle.Render(onOff(m.config.Account.SyncHistory)) + "\n")
		last := "never"
		if !m.lastSync.IsZero() {
			last = m.lastSync.Format("15:04:05")
		}
		b.WriteString(DetailLabelStyle.Render(padRight("Last synced", 14)) + DetailValueStyle.Render(last) + "\n")
		if m.status != "" {
			b.WriteString("\n" + SubtitleStyle.Render(m.status) + "\n")
		}
		b.WriteString("\n" + HelpStyle.Render("s: sync now • L: log out • tab: switch tab • D: downloads • q: quit"))

	case accSubmitting:
		b.WriteString(SubtitleStyle.Render("Signing in..."))

	default:
		b.WriteString(SubtitleStyle.Render("Log in with your Stremio account to pull your addons (and watch history)."))
		b.WriteString("\n")
		b.WriteString(SubtitleStyle.Render("cremio stores only a session token locally, never your password."))
		b.WriteString("\n\n")

		labels := []string{"Email", "Password", "authKey"}
		for i, ti := range m.inputs {
			cursor := "  "
			if m.focused && i == m.focus {
				cursor = "▶ "
			}
			b.WriteString(cursor + DetailLabelStyle.Render(padRight(labels[i], 10)) + ti.View() + "\n")
			if i == accPassIdx {
				b.WriteString(SubtitleStyle.Render("      (or leave email/password blank and paste a key below)") + "\n")
			}
		}

		if m.errMsg != "" {
			b.WriteString("\n" + ErrorStyle.Render(m.errMsg) + "\n")
		}
		b.WriteString("\n")
		if m.focused {
			b.WriteString(HelpStyle.Render("tab: next field • enter: sign in • esc: leave form"))
		} else {
			b.WriteString(HelpStyle.Render("enter: focus form • tab: switch tab • q: quit"))
		}
	}

	return lipgloss.NewStyle().Render(b.String())
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// friendlyAuthError trims Stremio's raw error into a short user-facing line.
func friendlyAuthError(err error) string {
	msg := err.Error()
	if isTransientErr(err) {
		return "Could not reach Stremio. Check your connection and try again."
	}
	return fmt.Sprintf("Login failed: %s", msg)
}

// isTransientErr reports whether an error is a network/timeout condition (as
// opposed to a genuine auth rejection from the API).
func isTransientErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, needle := range []string{
		"timeout", "deadline", "no such host", "connection refused",
		"connection reset", "network is unreachable", "eof", "dial tcp",
		"tls", "temporary failure",
	} {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
