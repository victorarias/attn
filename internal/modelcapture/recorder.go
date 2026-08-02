// Package modelcapture persists opt-in, profile-local terminal viewport
// observations for evaluating and training small local models.
package modelcapture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	schemaVersion = 1
	filePrefix    = "observations-"
	fileSuffix    = ".jsonl"
)

type Observation struct {
	CapturedAt    time.Time
	CaptureReason string
	SessionID     string
	Agent         string
	DaemonState   string
	StateReason   string
	Running       bool
	Cols          uint16
	Rows          uint16
	LastSeq       uint32
	ViewportText  string
}

type record struct {
	SchemaVersion  int       `json:"schema_version"`
	CapturedAt     time.Time `json:"captured_at"`
	CaptureReason  string    `json:"capture_reason"`
	SessionKey     string    `json:"session_key"`
	Agent          string    `json:"agent"`
	DaemonState    string    `json:"daemon_state"`
	StateReason    string    `json:"state_reason,omitempty"`
	Running        bool      `json:"running"`
	Cols           uint16    `json:"cols"`
	Rows           uint16    `json:"rows"`
	LastSeq        uint32    `json:"last_seq"`
	ViewportSHA256 string    `json:"viewport_sha256"`
	ViewportText   string    `json:"viewport_text"`
}

type sessionCursor struct {
	lastSampleAt time.Time
	lastState    string
	lastHash     string
}

type Recorder struct {
	dir string

	mu          sync.Mutex
	sessions    map[string]sessionCursor
	lastPruneAt time.Time
}

func New(dir string) *Recorder {
	return &Recorder{
		dir:      filepath.Clean(dir),
		sessions: make(map[string]sessionCursor),
	}
}

func (r *Recorder) Dir() string {
	if r == nil {
		return ""
	}
	return r.dir
}

// Due reports whether a snapshot is worth fetching. State changes are sampled
// immediately; otherwise each session is sampled at most once per interval.
func (r *Recorder) Due(sessionID, daemonState string, now time.Time, interval time.Duration) (string, bool) {
	if r == nil || strings.TrimSpace(sessionID) == "" {
		return "", false
	}
	if interval <= 0 {
		interval = time.Second
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cursor, ok := r.sessions[sessionID]
	if !ok {
		return "initial", true
	}
	if cursor.lastState != daemonState {
		return "state_change", true
	}
	if now.Sub(cursor.lastSampleAt) >= interval {
		return "interval", true
	}
	return "", false
}

// Record persists obs unless both its viewport and daemon state match the last
// sampled observation for this session. A deduplicated observation still
// advances the interval clock so an unchanged prompt is not fetched every poll.
func (r *Recorder) Record(obs Observation, maxBytes int64) (bool, error) {
	if r == nil {
		return false, errors.New("nil model capture recorder")
	}
	if strings.TrimSpace(obs.SessionID) == "" {
		return false, errors.New("model capture observation has no session id")
	}
	if obs.CapturedAt.IsZero() {
		obs.CapturedAt = time.Now().UTC()
	} else {
		obs.CapturedAt = obs.CapturedAt.UTC()
	}
	if strings.TrimSpace(obs.CaptureReason) == "" {
		obs.CaptureReason = "interval"
	}

	viewportHash := sha256Hex(obs.ViewportText)

	r.mu.Lock()
	defer r.mu.Unlock()
	cursor := r.sessions[obs.SessionID]
	if cursor.lastHash == viewportHash && cursor.lastState == obs.DaemonState {
		cursor.lastSampleAt = obs.CapturedAt
		r.sessions[obs.SessionID] = cursor
		return false, nil
	}

	if err := ensurePrivateDir(r.dir); err != nil {
		return false, err
	}
	path := r.hourlyPath(obs.CapturedAt)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return false, fmt.Errorf("open model capture file: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return false, fmt.Errorf("secure model capture file: %w", err)
	}

	entry := record{
		SchemaVersion:  schemaVersion,
		CapturedAt:     obs.CapturedAt,
		CaptureReason:  obs.CaptureReason,
		SessionKey:     sha256Hex(obs.SessionID),
		Agent:          strings.TrimSpace(strings.ToLower(obs.Agent)),
		DaemonState:    obs.DaemonState,
		StateReason:    obs.StateReason,
		Running:        obs.Running,
		Cols:           obs.Cols,
		Rows:           obs.Rows,
		LastSeq:        obs.LastSeq,
		ViewportSHA256: viewportHash,
		ViewportText:   obs.ViewportText,
	}
	encodeErr := json.NewEncoder(f).Encode(entry)
	closeErr := f.Close()
	if encodeErr != nil {
		return false, fmt.Errorf("write model capture record: %w", encodeErr)
	}
	if closeErr != nil {
		return false, fmt.Errorf("close model capture file: %w", closeErr)
	}

	r.sessions[obs.SessionID] = sessionCursor{
		lastSampleAt: obs.CapturedAt,
		lastState:    obs.DaemonState,
		lastHash:     viewportHash,
	}
	if maxBytes > 0 && (r.lastPruneAt.IsZero() || obs.CapturedAt.Sub(r.lastPruneAt) >= time.Hour) {
		if err := pruneLocked(r.dir, maxBytes, path); err != nil {
			return true, err
		}
		r.lastPruneAt = obs.CapturedAt
	}
	return true, nil
}

func (r *Recorder) hourlyPath(at time.Time) string {
	return filepath.Join(r.dir, filePrefix+at.UTC().Format("20060102-15")+fileSuffix)
}

func SizeBytes(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.IsDir() || !isCaptureFile(entry.Name()) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	return total, err
}

type captureFile struct {
	path    string
	name    string
	modTime time.Time
	size    int64
}

func pruneLocked(dir string, maxBytes int64, activePath string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read model capture directory: %w", err)
	}
	files := make([]captureFile, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !isCaptureFile(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat model capture file %s: %w", entry.Name(), err)
		}
		file := captureFile{
			path:    filepath.Join(dir, entry.Name()),
			name:    entry.Name(),
			modTime: info.ModTime(),
			size:    info.Size(),
		}
		files = append(files, file)
		total += file.size
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].modTime.Equal(files[j].modTime) {
			return files[i].name < files[j].name
		}
		return files[i].modTime.Before(files[j].modTime)
	})
	for _, file := range files {
		if total <= maxBytes {
			break
		}
		if file.path == activePath {
			continue
		}
		if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("prune model capture file %s: %w", file.name, err)
		}
		total -= file.size
	}
	return nil
}

func isCaptureFile(name string) bool {
	return strings.HasPrefix(name, filePrefix) && strings.HasSuffix(name, fileSuffix)
}

func ensurePrivateDir(dir string) error {
	if strings.TrimSpace(dir) == "" || dir == "." {
		return errors.New("model capture directory is empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create model capture directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure model capture directory: %w", err)
	}
	return nil
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
