package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/itssoap/cremio/internal/config"
	"github.com/itssoap/cremio/internal/history"
	"github.com/itssoap/cremio/internal/tui"
)

func main() {
	incognito := false
	for _, arg := range os.Args[1:] {
		if arg == "--incognito" || arg == "-incognito" {
			incognito = true
		}
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	hist, err := history.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading watch history: %v\n", err)
		os.Exit(1)
	}

	app := tui.NewApp(cfg, hist, incognito)
	p := tea.NewProgram(app, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
