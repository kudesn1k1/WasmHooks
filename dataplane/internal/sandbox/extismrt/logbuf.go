package extismrt

import (
	extism "github.com/extism/go-sdk"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox"
)

const truncatedMarker = "[truncated]"

// logBuffer collects one call's guest log lines up to a line and byte cap.
// Past the cap it records a single truncation marker and drops the rest.
type logBuffer struct {
	maxBytes, maxLines int

	lines     []sandbox.LogLine
	bytes     int
	truncated bool
}

func (b *logBuffer) add(level extism.LogLevel, msg string) {
	if b.truncated {
		return
	}
	if len(b.lines) >= b.maxLines || b.bytes+len(msg) > b.maxBytes {
		b.truncated = true
		b.lines = append(b.lines, sandbox.LogLine{Level: "warn", Message: truncatedMarker})
		return
	}
	b.bytes += len(msg)
	b.lines = append(b.lines, sandbox.LogLine{Level: levelName(level), Message: msg})
}

func (b *logBuffer) reset() {
	b.lines, b.bytes, b.truncated = nil, 0, false
}

// take returns the collected lines and leaves the buffer empty.
func (b *logBuffer) take() []sandbox.LogLine {
	lines := b.lines
	b.reset()
	return lines
}

func levelName(l extism.LogLevel) string {
	switch l {
	case extism.LogLevelTrace:
		return "trace"
	case extism.LogLevelDebug:
		return "debug"
	case extism.LogLevelInfo:
		return "info"
	case extism.LogLevelWarn:
		return "warn"
	default:
		return "error"
	}
}
