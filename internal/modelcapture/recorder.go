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
	"strconv"
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

	mu       sync.Mutex
	sessions map[string]sessionCursor
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
	payload, err := json.Marshal(entry)
	if err != nil {
		return false, fmt.Errorf("encode model capture record: %w", err)
	}
	payload = append(payload, byte(10))

	path := r.hourlyPath(obs.CapturedAt)
	if maxBytes > 0 {
		path, err = prepareAppendLocked(r.dir, obs.CapturedAt, maxBytes, int64(len(payload)))
		if err != nil {
			return false, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return false, fmt.Errorf("open model capture file: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return false, fmt.Errorf("secure model capture file: %w", err)
	}

	_, writeErr := f.Write(payload)
	closeErr := f.Close()
	if writeErr != nil {
		return false, fmt.Errorf("write model capture record: %w", writeErr)
	}
	if closeErr != nil {
		return false, fmt.Errorf("close model capture file: %w", closeErr)
	}

	r.sessions[obs.SessionID] = sessionCursor{
		lastSampleAt: obs.CapturedAt,
		lastState:    obs.DaemonState,
		lastHash:     viewportHash,
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

func prepareAppendLocked(dir string, at time.Time, maxBytes, appendBytes int64) (string, error) {
	if appendBytes > maxBytes {
		return "", fmt.Errorf("model capture record is %d bytes, larger than the %d-byte storage cap", appendBytes, maxBytes)
	}
	files, total, err := captureFilesLocked(dir)
	if err != nil {
		return "", err
	}

	basePath := filepath.Join(dir, filePrefix+at.UTC().Format("20060102-15")+fileSuffix)
	activePath, activeSize, nextPath := bucketAppendPaths(files, basePath)
	if total+appendBytes <= maxBytes {
		return activePath, nil
	}

	target := maxBytes - appendBytes
	protectedPath := activePath
	if activeSize+appendBytes > maxBytes {
		// The current segment cannot accept this complete JSONL record while the
		// corpus stays bounded. Close it by advancing to a sibling segment; it is
		// then eligible for oldest-first pruning like every other capture file.
		protectedPath = ""
		activePath = nextPath
	}
	remaining, err := pruneFilesLocked(files, total, target, protectedPath)
	if err != nil {
		return "", err
	}
	if remaining > target {
		return "", fmt.Errorf("model capture storage cannot reserve %d bytes under the %d-byte cap", appendBytes, maxBytes)
	}
	return activePath, nil
}

func captureFilesLocked(dir string) ([]captureFile, int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("read model capture directory: %w", err)
	}
	files := make([]captureFile, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !isCaptureFile(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, 0, fmt.Errorf("stat model capture file %s: %w", entry.Name(), err)
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
	return files, total, nil
}

func bucketAppendPaths(files []captureFile, basePath string) (string, int64, string) {
	baseName := filepath.Base(basePath)
	activePath := basePath
	var activeSize int64
	maxIndex := -1
	for _, file := range files {
		index, ok := captureSegmentIndex(file.name, baseName)
		if !ok || index <= maxIndex {
			continue
		}
		maxIndex = index
		activePath = file.path
		activeSize = file.size
	}
	nextIndex := maxIndex + 1
	if nextIndex < 1 {
		nextIndex = 1
	}
	stem := strings.TrimSuffix(basePath, fileSuffix)
	nextPath := fmt.Sprintf("%s-%04d%s", stem, nextIndex, fileSuffix)
	return activePath, activeSize, nextPath
}

func captureSegmentIndex(name, baseName string) (int, bool) {
	if name == baseName {
		return 0, true
	}
	stem := strings.TrimSuffix(baseName, fileSuffix)
	if !strings.HasPrefix(name, stem+"-") || !strings.HasSuffix(name, fileSuffix) {
		return 0, false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(name, stem+"-"), fileSuffix)
	index, err := strconv.Atoi(raw)
	return index, err == nil && index > 0
}

func pruneFilesLocked(files []captureFile, total, target int64, protectedPath string) (int64, error) {
	for _, file := range files {
		if total <= target {
			break
		}
		if file.path == protectedPath {
			continue
		}
		if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return total, fmt.Errorf("prune model capture file %s: %w", file.name, err)
		}
		total -= file.size
	}
	return total, nil
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
