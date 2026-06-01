# Cremio

<img src="cremio.png" width="300" />

A functional way to access Stremio. Cremio is a TUI client built in Go with [Bubbletea](https://github.com/charmbracelet/bubbletea) that talks to Stremio addons, lets you browse catalogs, search for movies and series, pick episodes, and fire streams straight into mpv. No browser, no Electron, no nonsense (_I really didn't want a UI_)

<img width="640" alt="showcase" src="https://github.com/user-attachments/assets/5161ab73-8b8c-4dad-a3d0-88c49e61f85f" />


`Disclaimer: This tool neither allows nor encourages streaming and distribution of pirated media. The tool reflects a Proof-of-concept of a Stremio client without any Graphical interface, and the usage in all aspects is subject to the Users' liability and not the tools'.`

## Quick Install

### Windows

```powershell
irm https://raw.githubusercontent.com/itssoap/cremio/main/install.ps1 | iex
```

### Linux / macOS / FreeBSD

```bash
curl -fsSL https://raw.githubusercontent.com/itssoap/cremio/main/install.sh | bash
```

Both scripts download the latest binary, install it to a local bin directory, and add it to your `PATH`. Run the same command again anytime to check for and install updates.

**Prerequisite:** [mpv](https://mpv.io/installation/) must be in your system `PATH`.

## Features

- Browse catalogs from all installed Stremio addons
- Cinemeta (v3) installed by default on first run; can be removed via the Addons tab
- Full-text search with automatic fallback to client-side filtering
- Optional auto-focus of the search bar when switching to the Search tab (set `auto_focus_search` in config)
- History tab for quick access to watched movies and shows
- Series support with season/episode navigation
- Stream resolution across multiple addons
- Playback via mpv (should be in PATH)
- Addon management (add/remove by URL with manifest validation)
- Persistent configuration stored as JSON

## Prerequisites

- **mpv** in your system `PATH` (for stream playback)
- **Go 1.25 or later** (only needed to build from source)
- **go-winres** (optional, only needed to embed a custom icon on Windows builds)

Pre-built Windows binaries are available on the [releases page](https://github.com/itssoap/cremio/releases). Use the [Quick Install](#quick-install) command above for the easiest setup.

## Development Setup

Clone the repository and install dependencies:

```
git clone https://github.com/itssoap/cremio.git
cd cremio
go mod tidy
```

Run the application directly:

```
go run .
```

## Compilation

Build a standalone binary:

```
go build -o cremio.exe .
```

On Linux or macOS, omit the `.exe` extension (I haven't tested the build on these systems, will need assistance for this):

```
go build -o cremio .
```

### Windows Icon Embedding

To embed a custom icon into the Windows executable, install go-winres and regenerate the resource files before building:

```
go install github.com/tc-hib/go-winres@latest
```

Place your icon as a PNG in `winres/icon.png` (max 256x256) and `winres/icon16.png` (16x16), then:

```
go-winres make
go build -o cremio.exe .
```

The generated `.syso` files are excluded from version control via `.gitignore` and must be regenerated locally.

## Configuration

Cremio stores its configuration as a JSON file:

- **Windows:** `%APPDATA%\cremio\config.json`
- **Linux/macOS:** `~/.config/cremio/config.json`

The config file holds the list of installed addon base URLs and optional settings. It is created automatically on first use. Cinemeta (v3) is added as the default addon on first run.

```json
{
  "addons": [
    "https://v3-cinemeta.strem.io/manifest.json"
  ],
  "auto_focus_search": false
}
```

### Configuration fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `addons` | array of strings | `[cinemeta URL]` | List of installed Stremio addon manifest URLs |
| `auto_focus_search` | boolean | `false` | When true, the search input is automatically focused when switching to the Search tab |

## Watch History

Cremio tracks watched movies and episodes in a local JSON file:

- **Windows:** `%APPDATA%\cremio\history.json`
- **Linux/macOS:** `~/.config/cremio/history.json`

Episodes are automatically marked as watched when played via mpv. You can also manually toggle watched status with the `w` key - on individual episodes, whole seasons, or movies.

The file uses a [Trakt](https://trakt.tv)-compatible structure, so it can be exported and imported directly via Trakt's `/sync/history` API:

```json
{
  "movies": [
    {
      "watched_at": "2026-05-26T12:00:00Z",
      "ids": { "imdb": "tt1234567" }
    }
  ],
  "shows": [
    {
      "ids": { "imdb": "tt7654321" },
      "seasons": [
        {
          "number": 1,
          "episodes": [
            { "number": 1, "watched_at": "2026-05-26T12:00:00Z" },
            { "number": 2, "watched_at": "2026-05-26T12:30:00Z" }
          ]
        }
      ]
    }
  ]
}
```

## Controls

| Key        | Action                                      |
|------------|---------------------------------------------|
| `tab`      | Cycle between Home, Search, Addons, and History tabs |
| `/`        | Focus the search input (Search tab)         |
| `enter`    | Select item / submit input                  |
| `esc`      | Go back / unfocus input                     |
| `w`        | Toggle watched (episode, season, or movie)  |
| `f`        | Fetch streams for all episodes (series)     |
| `a`        | Add a new addon (Addons tab)                |
| `d`        | Remove selected addon (Addons tab)          |
| `q`       | Quit                                         |
| `ctrl+c`   | Quit                                        |

## Project Structure

```
main.go                  Entry point
internal/
  config/config.go       Configuration loading, saving, addon management
  history/history.go     Watch history tracking and Trakt-compatible export
  player/mpv.go          mpv process launcher
  stremio/
    client.go            HTTP client for the Stremio Addon Protocol
    types.go             Manifest, catalog, meta, stream type definitions
  tui/
    app.go               Root Bubbletea model and screen routing
    home.go              Home screen with catalog browsing
    search.go            Search screen with addon querying
    addons.go            Addon management screen
    detail.go            Movie/series detail and episode list
    streams.go           Stream list and mpv launch
    historytab.go        History tab for browsing watched movies and shows
    styles.go            Lipgloss style definitions
winres/
  winres.json            Windows resource manifest for go-winres
  icon.png               Application icon (256x256)
  icon16.png             Application icon (16x16)
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Keep changes focused and minimal
4. Submit a pull request

Please avoid introducing new dependencies unless strictly necessary. Keep the codebase simple and the TUI responsive.

### Where to make changes

Use the table below to find the right file for what you want to improve:

| What you want to change | File(s) |
|-------------------------|---------|
| **Home tab** - catalog browsing, how items are loaded or displayed | `internal/tui/home.go` |
| **Search tab** - search input, result deduplication, client-side filtering | `internal/tui/search.go` |
| **Addons tab** - add/remove addons, URL validation, manifest display | `internal/tui/addons.go` |
| **Detail screen** - movie/series info layout, episode/season list, watched toggle | `internal/tui/detail.go` |
| **Streams screen** - stream list, filter, info panel, mpv launch, batch mode | `internal/tui/streams.go` |
| **History tab** - watched movies/shows list, navigation to detail | `internal/tui/historytab.go` |
| **Screen routing & global keys** - tab switching, ESC behaviour, app-level messages | `internal/tui/app.go` |
| **Colours, borders, text styles** | `internal/tui/styles.go` |
| **Stremio addon protocol** - HTTP client, endpoint logic | `internal/stremio/client.go` |
| **Stremio types** - manifest, catalog, meta, stream structs | `internal/stremio/types.go` |
| **Watch history** - toggle watched, Trakt-compatible JSON structure | `internal/history/history.go` |
| **Config** - addon list persistence, config file path | `internal/config/config.go` |
| **App data directory** - where config & history are stored | `internal/appdir/appdir.go` |
| **mpv integration** - launch flags, extra arguments | `internal/player/mpv.go` |
| **Windows executable icon / version metadata** | `winres/winres.json` |

## FAQ/Known issues

Q. Search is broken? I see results showing in Stremio, but not on this app.

A. Cinemeta is installed by default on first run. If you removed it, re-add it from the Addons tab. Some addons lack search capability in their catalogs by default.

Q. Sometimes the Search results appear blank, but it seems like I am able to navigate across them.

A. Please restart the app. This issue occurs very seldomly.

Q. It shows "▶ Launched", but I don't see the mpv window yet?

A. mpv takes its own sweet time to fetch the metadata and start the stream. Cremio triggers mpv as an independent process, so that killing cremio won't kill your stream.

## License

This project is licensed under the [MIT License](LICENSE).
