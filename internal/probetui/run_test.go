package probetui

import (
	"bytes"
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

func TestRunDrivesStartupResizeAndTeardown(t *testing.T) {
	var mu sync.Mutex
	cols, rows := 80, 24
	size := func() (int, int, error) {
		mu.Lock()
		defer mu.Unlock()
		return cols, rows, nil
	}

	var buf bytes.Buffer
	var writeMu sync.Mutex
	syncWriter := writerFunc(func(p []byte) (int, error) {
		writeMu.Lock()
		defer writeMu.Unlock()
		return buf.Write(p)
	})

	winch := make(chan os.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, syncWriter, StyleClaude, size, winch, 10*time.Millisecond)
	}()

	// Let at least one frame render at the initial geometry before resizing.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	cols, rows = 62, 27
	mu.Unlock()
	winch <- os.Interrupt // stand-in for SIGWINCH; Run only reads from the channel

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancellation")
	}

	writeMu.Lock()
	out := buf.Bytes()
	writeMu.Unlock()

	startup := Startup(StyleClaude, 80, 24)
	if !bytes.HasPrefix(out, startup) {
		t.Fatalf("output does not start with Startup(80,24)")
	}

	oldBanner := []byte(bannerGeometryRow(80, 24))
	if !bytes.Contains(out, oldBanner) {
		t.Fatalf("output missing a frame at the initial geometry (banner %q)", oldBanner)
	}

	resize := OnResize(StyleClaude, 62, 27)
	if !bytes.Contains(out, resize) {
		t.Fatalf("output missing the OnResize(62,27) sequence")
	}

	newBanner := []byte(bannerGeometryRow(62, 27))
	foundNewFrame := bytes.Contains(out, newBanner)
	if !foundNewFrame {
		t.Fatalf("output missing a frame at the resized geometry (62x27)")
	}

	teardown := Teardown(StyleClaude)
	if !bytes.HasSuffix(out, teardown) {
		t.Fatalf("output does not end with Teardown()")
	}

	// The resize sequence must appear before any frame at the new geometry.
	resizeIdx := bytes.Index(out, resize)
	newFrameIdx := bytes.Index(out, newBanner)
	if resizeIdx < 0 || newFrameIdx < 0 || resizeIdx > newFrameIdx {
		t.Fatalf("OnResize must precede the first frame at the new geometry, got resizeIdx=%d newFrameIdx=%d", resizeIdx, newFrameIdx)
	}
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
