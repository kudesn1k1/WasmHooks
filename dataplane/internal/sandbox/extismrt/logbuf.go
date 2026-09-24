package extismrt

import (
	"unicode/utf8"

	extism "github.com/extism/go-sdk"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox"
)

const truncatedMarker = "[truncated]"

// logBuffer collects one call's guest log lines up to a line and byte cap.
// A line that does not fit the remaining bytes is cut to fit; past either
// cap a single truncation marker is recorded and the rest is dropped.
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
	if len(b.lines) >= b.maxLines {
		b.stop()
		return
	}
	if room := b.maxBytes - b.bytes; len(msg) > room {
		if cut := cutUTF8(msg, room); cut != "" {
			b.lines = append(b.lines, sandbox.LogLine{Level: levelName(level), Message: cut})
		}
		b.stop()
		return
	}
	b.bytes += len(msg)
	b.lines = append(b.lines, sandbox.LogLine{Level: levelName(level), Message: msg})
}

func (b *logBuffer) stop() {
	b.truncated = true
	b.lines = append(b.lines, sandbox.LogLine{Level: "warn", Message: truncatedMarker})
}

// cutUTF8 returns the longest prefix of s of at most n bytes that does not
// split a rune.
func cutUTF8(s string, n int) string {
	if n <= 0 {
		return ""
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
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
