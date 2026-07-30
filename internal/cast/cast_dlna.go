//go:build cast

package cast

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/huin/goupnp/dcps/av1"
)

// dlnaRescanInterval is how often SSDP is re-run so devices appearing/leaving
// the network show up in the picker ("keeps polling").
var dlnaRescanInterval = 5 * time.Second

// avTransport is the subset of av1.AVTransport1 the renderer uses. Extracted as
// an interface so the state logic can be unit-tested with a fake. The concrete
// *av1.AVTransport1 satisfies it.
type avTransport interface {
	SetAVTransportURI(instanceID uint32, currentURI, currentURIMetaData string) error
	Play(instanceID uint32, speed string) error
	Pause(instanceID uint32) error
	Stop(instanceID uint32) error
	GetTransportInfo(instanceID uint32) (state, status, speed string, err error)
}

// dlnaBackend discovers DLNA/UPnP MediaRenderers (SSDP) and controls them via
// the AVTransport service. The user switches audio/subtitle tracks with the TV
// remote; DLNA has no standard track-selection call and cremio does not try to.
type dlnaBackend struct {
	mu      sync.Mutex
	clients map[string]*av1.AVTransport1 // UDN -> control client (latest scan)
}

func newDLNABackend() *dlnaBackend {
	return &dlnaBackend{clients: make(map[string]*av1.AVTransport1)}
}

func (b *dlnaBackend) Handles(kind DeviceKind) bool { return kind == KindDLNA }

func (b *dlnaBackend) Discover(ctx context.Context) (<-chan []Device, error) {
	out := make(chan []Device)
	go func() {
		defer close(out)
		t := time.NewTicker(dlnaRescanInterval)
		defer t.Stop()
		b.scan(ctx, out) // immediate first scan
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				b.scan(ctx, out)
			}
		}
	}()
	return out, nil
}

func (b *dlnaBackend) scan(ctx context.Context, out chan<- []Device) {
	clients, _, err := av1.NewAVTransport1ClientsCtx(ctx)
	if err != nil {
		return // transient SSDP error; keep the last known list, try again next tick
	}
	devs := make([]Device, 0, len(clients))
	fresh := make(map[string]*av1.AVTransport1, len(clients))
	for _, c := range clients {
		if c.RootDevice == nil {
			continue
		}
		udn := c.RootDevice.Device.UDN
		if udn == "" {
			continue
		}
		fresh[udn] = c
		addr := ""
		if c.Location != nil {
			addr = c.Location.Host
		}
		name := c.RootDevice.Device.FriendlyName
		if name == "" {
			name = addr
		}
		devs = append(devs, Device{ID: udn, Name: name, Addr: addr, Kind: KindDLNA})
	}
	b.mu.Lock()
	b.clients = fresh
	b.mu.Unlock()
	select {
	case out <- devs:
	case <-ctx.Done():
	}
}

func (b *dlnaBackend) Connect(_ context.Context, dev Device) (renderer, error) {
	b.mu.Lock()
	c := b.clients[dev.ID]
	b.mu.Unlock()
	if c == nil {
		return nil, fmt.Errorf("dlna: device %q not found; rescan and retry", dev.Name)
	}
	return &dlnaRenderer{tr: c}, nil
}

// dlnaRenderer controls one DLNA renderer via AVTransport.
type dlnaRenderer struct {
	tr avTransport

	mu      sync.Mutex
	started bool // seen a non-stopped state since the last Load
}

func (r *dlnaRenderer) Load(_ context.Context, item MediaItem) error {
	if err := r.tr.SetAVTransportURI(0, item.URL, didlLite(item)); err != nil {
		return err
	}
	r.mu.Lock()
	r.started = false
	r.mu.Unlock()
	return r.tr.Play(0, "1")
}

func (r *dlnaRenderer) Play() error  { return r.tr.Play(0, "1") }
func (r *dlnaRenderer) Pause() error { return r.tr.Pause(0) }
func (r *dlnaRenderer) Stop() error  { return r.tr.Stop(0) }
func (r *dlnaRenderer) Close() error { return nil }

// State maps AVTransport transport state to the queue manager's states. A
// freshly-loaded item briefly reports STOPPED before playback starts; reporting
// that as "ended" would advance the queue prematurely, so STOPPED counts as
// ended only after a playing/paused state has been observed since the last Load.
func (r *dlnaRenderer) State() (playbackState, error) {
	state, _, _, err := r.tr.GetTransportInfo(0)
	if err != nil {
		return stateUnknown, err
	}
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "PLAYING", "TRANSITIONING":
		r.mu.Lock()
		r.started = true
		r.mu.Unlock()
		return statePlaying, nil
	case "PAUSED_PLAYBACK", "PAUSED_RECORDING":
		r.mu.Lock()
		r.started = true
		r.mu.Unlock()
		return statePaused, nil
	case "STOPPED", "NO_MEDIA_PRESENT":
		r.mu.Lock()
		started := r.started
		r.mu.Unlock()
		if started {
			return stateEnded, nil
		}
		return statePlaying, nil // just loaded, not playing yet: do not advance
	default:
		return stateUnknown, nil
	}
}

// didlLite builds minimal DIDL-Lite metadata for SetAVTransportURI. Many
// renderers need it (with a protocolInfo mime) to accept and correctly play a
// URL. Inner values are XML-escaped; goupnp escapes the whole string for SOAP.
func didlLite(item MediaItem) string {
	title := item.Title
	if title == "" {
		title = "Video"
	}
	mime := item.MimeType
	if mime == "" {
		mime = guessMimeFromURL(item.URL)
	}
	protocolInfo := "http-get:*:" + mime + ":*"
	var b strings.Builder
	b.WriteString(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" `)
	b.WriteString(`xmlns:dc="http://purl.org/dc/elements/1.1/" `)
	b.WriteString(`xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">`)
	b.WriteString(`<item id="0" parentID="-1" restricted="1">`)
	b.WriteString("<dc:title>" + xmlEscape(title) + "</dc:title>")
	b.WriteString("<upnp:class>object.item.videoItem</upnp:class>")
	b.WriteString(`<res protocolInfo="` + xmlEscape(protocolInfo) + `">` + xmlEscape(item.URL) + "</res>")
	b.WriteString("</item></DIDL-Lite>")
	return b.String()
}

func guessMimeFromURL(u string) string {
	clean := u
	if i := strings.IndexAny(clean, "?#"); i >= 0 {
		clean = clean[:i]
	}
	switch strings.ToLower(path.Ext(clean)) {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".avi":
		return "video/x-msvideo"
	case ".ts":
		return "video/mp2t"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	default:
		return "video/mp4"
	}
}

var xmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&apos;",
)

func xmlEscape(s string) string { return xmlEscaper.Replace(s) }
