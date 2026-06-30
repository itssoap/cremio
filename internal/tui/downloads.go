package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/itssoap/cremio/internal/player"
)

// downloadTickMsg is sent periodically to refresh download progress.
type downloadTickMsg struct{}

// downloadJobDoneMsg is sent when a job finishes (success, fail, skip, cancel).
type downloadJobDoneMsg struct{ jobID int }

// DownloadsModel is the popup overlay showing active/queued/done downloads.
type DownloadsModel struct {
	manager  *player.DownloadManager
	visible  bool
	cursor   int
	scroll   int
	width    int
	height   int
}

func NewDownloadsModel(mgr *player.DownloadManager) DownloadsModel {
	return DownloadsModel{manager: mgr}
}

func (m *DownloadsModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *DownloadsModel) Toggle() {
	m.visible = !m.visible
	if m.visible {
		m.cursor = 0
		m.scroll = 0
	}
}

func (m *DownloadsModel) IsVisible() bool {
	return m.visible
}

func (m DownloadsModel) Update(msg tea.Msg) (DownloadsModel, tea.Cmd) {
	if !m.visible {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		jobs := m.manager.Jobs()
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				if m.cursor < m.scroll {
					m.scroll = m.cursor
				}
			}
		case "down", "j":
			if m.cursor < len(jobs)-1 {
				m.cursor++
				visible := m.visibleRows()
				if m.cursor >= m.scroll+visible {
					m.scroll = m.cursor - visible + 1
				}
			}
		case "d":
			// Cancel selected job
			if m.cursor < len(jobs) {
				m.manager.CancelJob(jobs[m.cursor].ID)
			}
			return m, func() tea.Msg { return downloadJobDoneMsg{} }
		case "x":
			// Cancel all
			m.manager.CancelAll()
			return m, func() tea.Msg { return downloadJobDoneMsg{} }
		case "c":
			// Clear finished
			m.manager.ClearDone()
			m.cursor = 0
			m.scroll = 0
		case "esc", "D":
			m.visible = false
		}
	}
	return m, nil
}

func (m DownloadsModel) visibleRows() int {
	// Reserve lines for header (3) + footer (2) + border (2)
	rows := m.height - 9
	if rows < 3 {
		rows = 3
	}
	return rows
}

func (m DownloadsModel) View() string {
	if !m.visible {
		return ""
	}

	jobs := m.manager.Jobs()
	w := m.width - 6
	if w < 40 {
		w = 40
	}

	var b strings.Builder

	// Header
	b.WriteString(TitleStyle.Render("Downloads"))
	b.WriteString("\n\n")

	if len(jobs) == 0 {
		b.WriteString(HelpStyle.Render("  No downloads"))
		b.WriteString("\n")
	} else {
		// Stats
		var active, queued, done, failed int
		var totalBytes, doneBytes int64
		var totalSpeed int64
		for _, j := range jobs {
			switch j.State {
			case player.StateActive:
				active++
				totalSpeed += j.Speed
				doneBytes += j.BytesRead
				if j.TotalBytes > 0 {
					totalBytes += j.TotalBytes
				}
			case player.StateQueued:
				queued++
			case player.StateDone:
				done++
				doneBytes += j.BytesRead
				totalBytes += j.BytesRead
			case player.StateFailed:
				failed++
			}
		}

		visRows := m.visibleRows()
		end := m.scroll + visRows
		if end > len(jobs) {
			end = len(jobs)
		}

		for i := m.scroll; i < end; i++ {
			j := jobs[i]
			cursor := "  "
			if i == m.cursor {
				cursor = "▶ "
			}

			var line string
			switch j.State {
			case player.StateQueued:
				line = fmt.Sprintf("%s── %s  queued", cursor, j.Label)
			case player.StateActive:
				bar := m.progressBar(j, w-len(cursor)-len(j.Label)-20)
				speed := player.FormatBytes(j.Speed) + "/s"
				line = fmt.Sprintf("%s⬇ %s  %s  %s", cursor, j.Label, bar, speed)
			case player.StateDone:
				size := player.FormatBytes(j.BytesRead)
				line = fmt.Sprintf("%s✓ %s  %s  done", cursor, j.Label, size)
			case player.StateFailed:
				errMsg := ""
				if j.Err != nil {
					errMsg = j.Err.Error()
					if len(errMsg) > 30 {
						errMsg = errMsg[:30] + "..."
					}
				}
				line = fmt.Sprintf("%s✗ %s  %s", cursor, j.Label, errMsg)
			case player.StateCancelled:
				line = fmt.Sprintf("%s⊘ %s  cancelled", cursor, j.Label)
			case player.StateSkipped:
				line = fmt.Sprintf("%s⊘ %s  %s", cursor, j.Label, j.SkipReason)
			}

			if i == m.cursor {
				b.WriteString(SubtitleStyle.Render(line))
			} else {
				b.WriteString(line)
			}
			b.WriteString("\n")
		}

		// Scroll indicator
		if len(jobs) > visRows {
			b.WriteString(HelpStyle.Render(fmt.Sprintf("  (%d/%d)", m.cursor+1, len(jobs))))
			b.WriteString("\n")
		}

		// Summary
		b.WriteString("\n")
		summary := fmt.Sprintf("  Active: %d │ Queued: %d │ Done: %d │ Failed: %d", active, queued, done, failed)
		if totalSpeed > 0 {
			summary += fmt.Sprintf(" │ %s/s", player.FormatBytes(totalSpeed))
		}
		b.WriteString(HelpStyle.Render(summary))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(HelpStyle.Render("d: cancel selected • x: cancel all • c: clear done • esc: close"))

	// Render in a bordered box
	content := b.String()
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(0, 1).
		Width(w)

	return boxStyle.Render(content)
}

func (m DownloadsModel) progressBar(j *player.DownloadJob, width int) string {
	if width < 10 {
		width = 10
	}
	if j.TotalBytes <= 0 {
		// Indeterminate: show bytes read
		return player.FormatBytes(j.BytesRead)
	}

	filled := int(float64(width) * j.Progress())
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	pct := int(j.Progress() * 100)
	return fmt.Sprintf("%s %d%%", bar, pct)
}
