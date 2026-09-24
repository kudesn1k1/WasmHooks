package extismrt

import (
	"context"
	"strings"
	"testing"

	extism "github.com/extism/go-sdk"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox/sandboxtest"
)

func newTestRuntime(t *testing.T) sandbox.Runtime {
	rt, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(context.Background()) })
	return rt
}

func TestContract(t *testing.T) {
	sandboxtest.Run(t, newTestRuntime)
}

func TestLogBufferCapsLines(t *testing.T) {
	b := logBuffer{maxBytes: 1 << 20, maxLines: 100}
	for range 150 {
		b.add(extism.LogLevelInfo, "0123456789")
	}
	lines := b.take()
	if len(lines) != 101 {
		t.Fatalf("got %d lines, want 100 + marker", len(lines))
	}
	if last := lines[100]; last.Message != truncatedMarker || last.Level != "warn" {
		t.Fatalf("last line = %+v", last)
	}
}

func TestLogBufferCapsBytes(t *testing.T) {
	b := logBuffer{maxBytes: 16 << 10, maxLines: 1000}
	line := strings.Repeat("x", 1000)
	for range 20 {
		b.add(extism.LogLevelWarn, line)
	}
	lines := b.take()
	if len(lines) != 17 {
		t.Fatalf("got %d lines, want 16 + marker", len(lines))
	}
	markers := 0
	for _, l := range lines {
		if l.Message == truncatedMarker {
			markers++
		}
	}
	if markers != 1 {
		t.Fatalf("got %d markers, want exactly 1", markers)
	}
}

func TestLogBufferTakeResets(t *testing.T) {
	b := logBuffer{maxBytes: 10, maxLines: 10}
	b.add(extism.LogLevelInfo, "0123456789x") // over the byte cap
	b.take()
	b.add(extism.LogLevelDebug, "ok")
	lines := b.take()
	if len(lines) != 1 || lines[0].Message != "ok" || lines[0].Level != "debug" {
		t.Fatalf("after take: %+v", lines)
	}
	if len(b.take()) != 0 {
		t.Fatal("take must leave the buffer empty")
	}
}
