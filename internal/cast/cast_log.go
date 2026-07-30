//go:build cast

package cast

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Cast logging records discovery, connect, and load steps to help diagnose
// device issues. In the cast build it is ON by default and writes to a
// timestamped file in the current working directory, e.g.
// cremio-cast-20060102-150405.log, created lazily on the first cast activity
// (so opening the app without casting writes nothing). Override the location by
// setting CREMIO_CAST_LOG to a file path, or disable with CREMIO_CAST_LOG=off
// (also 0/false/no). It never writes to stdout/stderr, so the TUI is undisturbed.
var castLog = &castLogger{}

type castLogger struct {
	once     sync.Once
	mu       sync.Mutex
	f        *os.File
	disabled bool
}

func (l *castLogger) open() {
	env := strings.TrimSpace(os.Getenv("CREMIO_CAST_LOG"))
	switch strings.ToLower(env) {
	case "off", "0", "false", "no":
		l.disabled = true
		return
	}
	path := env
	if path == "" {
		path = filepath.Join(".", "cremio-cast-"+time.Now().Format("20060102-150405")+".log")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		l.disabled = true
		return
	}
	l.f = f
}

func castLogf(format string, args ...any) {
	castLog.once.Do(castLog.open)
	if castLog.disabled || castLog.f == nil {
		return
	}
	castLog.mu.Lock()
	defer castLog.mu.Unlock()
	fmt.Fprintf(castLog.f, time.Now().Format("15:04:05.000")+" "+format+"\n", args...)
}
