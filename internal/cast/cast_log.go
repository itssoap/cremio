//go:build cast

package cast

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Cast logging is opt-in and off by default. Set CREMIO_CAST_LOG to a file path
// (e.g. CREMIO_CAST_LOG=cast.log) to record discovery, connect, and load steps
// for diagnosing device issues. It never writes to stdout/stderr, so it does
// not disturb the TUI.
var castLog = newCastLogger()

type castLogger struct {
	mu sync.Mutex
	f  *os.File
}

func newCastLogger() *castLogger {
	path := os.Getenv("CREMIO_CAST_LOG")
	if path == "" {
		return &castLogger{}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return &castLogger{}
	}
	return &castLogger{f: f}
}

func castLogf(format string, args ...any) {
	if castLog.f == nil {
		return
	}
	castLog.mu.Lock()
	defer castLog.mu.Unlock()
	fmt.Fprintf(castLog.f, time.Now().Format("15:04:05.000")+" "+format+"\n", args...)
}
