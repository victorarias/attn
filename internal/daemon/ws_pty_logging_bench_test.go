package daemon

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/logging"
)

// Quantifies the per-output-chunk log work WS-4 gates behind DEBUG: a
// previewBinaryForLog(data) allocation (which copies the whole chunk via
// string(data) before truncating to 32 bytes) plus a mutex-held synchronous
// Infof disk write. With DEBUG off this entire cost is removed for every chunk,
// for every attached client. Compare against BenchmarkPtyChunkLog_GatedOff,
// which is the new (debug-off) path: a single branch.
func benchPtyChunkLog(b *testing.B, chunk []byte) {
	logger, err := logging.New(filepath.Join(b.TempDir(), "bench.log"))
	if err != nil {
		b.Fatal(err)
	}
	defer logger.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Infof(
			"pty_output forward: id=%s seq=%d bytes=%d preview=%q",
			"sess-1234abcd",
			i,
			len(chunk),
			previewBinaryForLog(chunk),
		)
	}
}

func BenchmarkPtyChunkLog_64B(b *testing.B) { benchPtyChunkLog(b, bytes.Repeat([]byte("abcd"), 16)) }
func BenchmarkPtyChunkLog_1KB(b *testing.B) { benchPtyChunkLog(b, bytes.Repeat([]byte("abcd"), 256)) }
func BenchmarkPtyChunkLog_4KB(b *testing.B) { benchPtyChunkLog(b, bytes.Repeat([]byte("abcd"), 1024)) }

// The debug-off path after WS-4: building no args, taking no lock, writing
// nothing — just the gate branch.
func BenchmarkPtyChunkLog_GatedOff(b *testing.B) {
	debugLogging := false
	chunk := bytes.Repeat([]byte("abcd"), 1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if debugLogging {
			_ = previewBinaryForLog(chunk)
		}
	}
}
