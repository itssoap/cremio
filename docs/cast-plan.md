# Cast to device (optional feature) - plan

## Goal

Let a user cast the currently-viewable streams to another device on the LAN (a
TV, a Chromecast, a DLNA renderer), so the receiving device decodes and plays
the video instead of the local machine. Casting is offered from the streams
page (single stream or filtered list). In playlist mode the whole playlist is
cast in order.

The feature is OPTIONAL at build time: two builds are produced.
- Default build: no cast code, no cast dependencies, smaller binary. `shift+c`
  does nothing, exactly like today.
- Cast build: full feature compiled in.

## UX

- On the streams screen (single title or after filtering), `shift+c` opens a
  POPUP overlay inside the TUI (like the downloads popup `D`), not a new screen.
- The popup lists cast devices discovered on the LAN. The list keeps polling
  (re-discovers every ~2s) so devices appear/disappear live.
- Up/down to move, enter to cast to the highlighted device, esc to close.
- What gets cast:
  - Normal: the selected stream's URL.
  - Playlist mode (`config.PlaylistEnabled()`): the same URL list cremio builds
    for mpv (`buildPlaylist`) is sent as an ordered queue so episodes play in
    sequence.
- While a cast session is active the popup shows basic transport state
  (device, current item, play/pause/stop). Minimal controls: space play/pause,
  s stop, n/p next/prev (cast build only).

## Hard reality: cremio does not transcode

Casting hands the device a URL; the device must support the container and
codecs. cremio has no ffmpeg pipeline and will not transcode. So:
- Only HTTP/HTTPS stream URLs are castable. Magnet/torrent/`infoHash` streams
  and `externalUrl`-only entries are not castable and are hidden/greyed in the
  cast flow (reuse the existing http(s) gate + `Stream.IsContent`).
- If the device cannot decode a given container/codec (Chromecast is picky
  about MKV/HEVC depending on model; most DLNA smart TVs handle more), playback
  fails on the device. cremio surfaces the device error; it does not fall back
  to transcoding. This is a documented limitation, not a bug.

## Technology choice

Backend behind a small interface so more protocols can be added later.

- v1: Google Cast (Chromecast / Cast-enabled TVs) via
  `github.com/vishen/go-chromecast` (MIT). It provides mDNS/DNS-SD discovery,
  media LOAD, and QUEUE for playlists, plus transport control. Best-maintained
  Go option and covers Chromecast built-in TVs.
- Later (optional): DLNA/UPnP (SSDP discovery + AVTransport SOAP) for the large
  set of generic smart TVs that are not Cast-enabled. Broader coverage, more
  code. The interface is designed so this slots in without touching the TUI.

Discovery polling = periodic re-browse of the relevant service type.

## Optionality via Go build tags (the core requirement)

New package `internal/cast`:

    internal/cast/
      cast.go            // no build tag: Device, Caster interface, shared types
      cast_stub.go       // //go:build !cast : Available=false, no-op backend, ZERO deps
      cast_chromecast.go // //go:build cast  : real backend, imports go-chromecast

- `cast.Available` is a build-tag constant (`false` in the default build, `true`
  in the cast build).
- `cast.New()` returns the real caster in the cast build, a no-op caster in the
  default build.
- Because `go-chromecast` (and its protobuf / zeroconf deps) is imported ONLY
  from `cast_chromecast.go` under `//go:build cast`, the default `go build`
  never compiles or links it. Smaller binary, no new attack surface.

Interface (shared, dependency-free):

    type Device struct {
        ID    string // stable id (e.g. mDNS instance / UUID)
        Name  string // friendly name shown in the popup
        Addr  string // ip:port
        Kind  string // "chromecast" (later: "dlna")
    }

    type Caster interface {
        // Discover streams devices as they are found; cancel via ctx.
        Discover(ctx context.Context) (<-chan []Device, error)
        // Cast one URL (mimeType may be "").
        Cast(ctx context.Context, dev Device, url, title, mimeType string) error
        // CastQueue casts an ordered list starting at startIndex (playlist).
        CastQueue(ctx context.Context, dev Device, items []MediaItem, startIndex int) error
        Play(dev Device) error
        Pause(dev Device) error
        Stop(dev Device) error
        Next(dev Device) error
        Prev(dev Device) error
    }

The stub implements all methods as no-ops returning `ErrCastUnavailable`, and
`Discover` returns a closed/empty channel.

## TUI wiring

- `shift+c` is handled in the streams key path. Guard the whole thing with
  `cast.Available`:
  - Default build: `cast.Available == false`, so `shift+c` falls through and
    does nothing (identical to today).
  - Cast build: opens `CastPopup`.
- `CastPopup` mirrors `DownloadsModel`: an overlay model with `IsVisible()`,
  `View()` composited over the streams view, its own key handling while open.
- Polling: on open, start `Discover`; a `tea.Tick` (~2s) merges the latest
  device slice into the popup list. Closing cancels the discovery ctx.
- Casting:
  - Resolve castable URL(s) from the current selection using the existing
    http(s) gate. Non-castable selection => popup shows "not castable".
  - Single: `Cast(url)`. Playlist mode: `CastQueue(buildPlaylist(...))`.
- App-level: the popup, like downloads, is reachable while on the streams
  screen; `q`/esc semantics match existing popups (esc closes, never quits).

## CI / release

`build.yml` currently builds one binary per platform. Add a second variant with
`-tags cast`:
- Either a matrix axis `variant: [default, cast]` producing
  `cremio-<platform>` and `cremio-cast-<platform>`, or a separate job.
- Windows cast builds still run `go-winres make`.
- Release attaches both sets so users choose. README documents the difference
  and that the cast build is larger and does LAN device discovery (mDNS).
- `go test ./...` runs for both variants; the cast backend gets a small unit
  test behind `//go:build cast` (discovery parsing / queue building), the stub
  gets a test asserting `Available==false` and no-op behaviour.

## Security / privacy

- Only cast http(s) URLs (reuse the download gate). Never hand a magnet or a
  `-`-prefixed value to a device.
- Discovery is passive mDNS on the local network; no data leaves the LAN except
  the media URL sent to the chosen device (which then fetches it directly).
- No secrets involved. The cast build adds LAN discovery; documented so a
  privacy-conscious user can pick the default build.
- Device responses are untrusted input; parse defensively (names/addresses).

## Phases

1. Skeleton (DONE): `internal/cast` package only, imported by nothing. `cast.go`
   (types + `Caster`/`Session` interfaces + shared no-op), `cast_off.go`
   (`//go:build !cast`, `Available=false`), `cast_on.go` (`//go:build cast`,
   `Available=true`, no-op backend for now). No dependency added; both build
   variants compile and test. The `shift+c` gate was moved to Phase 2 (wiring a
   key to a not-yet-built popup is throwaway), so Phase 1 touches zero existing
   files and the default build is behaviourally identical.
2. CastPopup model + `shift+c` ("C") gated on `cast.Available`, app-level like
   the downloads popup, only while on the streams screen; polling discovery UI
   against the stub (shows "no devices / cast unavailable in this build").
3. `cast_on.go` real backends behind `//go:build cast`: go-chromecast (mDNS) +
   DLNA/UPnP (SSDP + AVTransport), merged discovery + single Cast.
4. Playlist: `CastQueue` from `buildPlaylist`. Sequence casting works.
5. Transport controls in the popup (play/pause/stop/next/prev) via `Session`.
6. CI: dual-variant build + release assets (`cremio-cast-<platform>`); README docs.

## Open questions

Resolved: (1) both Chromecast and DLNA/UPnP for maximum device coverage; (2) CI
builds both variants for every release; (3) asset naming `cremio-cast-<platform>`.
