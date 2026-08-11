// Package enrollment answers two questions about a daemon's data dir from two
// files that sit beside each other: who this daemon is (`daemon-id`) and whose
// it is (`enrollment.json`).
//
// A record naming the daemon itself means a home daemon — it owns its garden,
// its crew, and every other piece of user-level shared state. A record naming
// another daemon means an outpost of that home, which owns none of it.
// Status.RequireHome is the fence: the single place code asks whether
// home-level state may live here, so nothing has to check the record ad hoc.
//
// Both files are written under an flock on a sibling `.lock` file, because a
// daemon starting on the outpost and a home enrolling it over ssh reach the
// same directory at the same time.
//
// Design: docs/plans/2026-08-10-home-garden-crew-arc.md
package enrollment

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	// DaemonIDFileName holds the daemon's own durable `d-<32 hex>` identity.
	DaemonIDFileName = "daemon-id"
	// RecordFileName holds the enrollment record, beside the daemon id.
	RecordFileName = "enrollment.json"
	// PlanPath is quoted in refusals so whoever hits one can read why.
	PlanPath = "docs/plans/2026-08-10-home-garden-crew-arc.md"
)

// ErrNoRecord reports a data dir with no enrollment record at all. Every daemon
// writes one at startup, so this means the daemon has never run here, or the
// file was removed by hand.
var ErrNoRecord = errors.New("no enrollment record")

// ErrNoDaemonID reports a data dir with no daemon id: nothing has ever started
// here, so there is no identity to enroll or release.
var ErrNoDaemonID = errors.New("no daemon id")

// Record is the on-disk enrollment record. A home daemon's record names the
// daemon itself; an outpost's names its home.
type Record struct {
	HomeDaemonID string `json:"home_daemon_id"`
	RecordedAt   string `json:"recorded_at,omitempty"`
}

// Status is the resolved relationship: this daemon's id, and the id of the
// daemon that owns its home-level state.
type Status struct {
	DaemonID     string `json:"daemon_id"`
	HomeDaemonID string `json:"home_daemon_id"`
}

// IsHome reports whether this daemon is its own home.
func (s Status) IsHome() bool {
	return s.DaemonID != "" && s.DaemonID == s.HomeDaemonID
}

// Describe renders the relationship the way health output and logs say it.
func (s Status) Describe() string {
	if s.IsHome() {
		return "home"
	}
	if strings.TrimSpace(s.HomeDaemonID) == "" {
		return "unknown"
	}
	return "outpost of " + s.HomeDaemonID
}

// RequireHome is the fence. Garden, crew, and anything else the home owns asks
// it before touching state; surface names the thing being refused, in words a
// reader recognises ("the garden", "the crew roster").
func (s Status) RequireHome(surface string) error {
	if s.IsHome() {
		return nil
	}
	return &FencedError{Surface: surface, DaemonID: s.DaemonID, HomeDaemonID: s.HomeDaemonID}
}

// FencedError is what an outpost answers when asked to hold home-level state.
// It names what refused, why, and both ways forward — run it at home, or make
// this daemon a home again.
type FencedError struct {
	Surface      string
	DaemonID     string
	HomeDaemonID string
}

func (e *FencedError) Error() string {
	surface := strings.TrimSpace(e.Surface)
	if surface == "" {
		surface = "this state"
	}
	home := strings.TrimSpace(e.HomeDaemonID)
	if home == "" {
		return fmt.Sprintf(
			"refused %s on this daemon: its enrollment record is unreadable, so attn cannot tell whether this is a home.\n"+
				"  this daemon: %s\n"+
				"Run `attn enrollment` here to see the record, then `attn enrollment leave` to declare this daemon its own home.\n"+
				"Why: %s",
			surface, displayID(e.DaemonID), PlanPath,
		)
	}
	return fmt.Sprintf(
		"refused %s on this daemon: it is an outpost, and home-level state lives at its home.\n"+
			"  this daemon: %s (outpost)\n"+
			"  its home:    %s\n"+
			"The garden and the crew have exactly one owner — the home daemon — and the uplink that would\n"+
			"carry this ask home is not built yet.\n"+
			"Do this on the home daemon (%s), or make this daemon its own home again with `attn enrollment leave`.\n"+
			"Why: %s",
		surface, displayID(e.DaemonID), home, home, PlanPath,
	)
}

func displayID(id string) string {
	if strings.TrimSpace(id) == "" {
		return "unknown"
	}
	return id
}

// ForeignHomeError is the re-home refusal: this daemon is already enrolled to a
// home, and a different one asked to take it. Enrollment is never overwritten
// silently, so the decision goes back to whoever made it.
type ForeignHomeError struct {
	DaemonID     string
	CurrentHome  string
	RequestedBy  string
	DataRootHint string
}

func (e *ForeignHomeError) Error() string {
	return fmt.Sprintf(
		"this daemon (%s) is already an outpost of %s; %s asked to take it over.\n"+
			"Enrollment is never overwritten silently: a daemon has exactly one home, and moving it moves its\n"+
			"garden and crew asks with it.\n"+
			"To move it, run `attn enrollment leave` here — that makes it a home again — then sync it from %s.\n"+
			"Why: %s",
		displayID(e.DaemonID), e.CurrentHome, e.RequestedBy, e.RequestedBy, PlanPath,
	)
}

// Result is what a write returns, and the JSON `attn enrollment` prints. The
// hub reads it back over ssh, so the wording lands in the home's log verbatim.
type Result struct {
	// Status is one of: enrolled, unchanged, left, refused.
	Status       string `json:"status"`
	DaemonID     string `json:"daemon_id"`
	HomeDaemonID string `json:"home_daemon_id"`
	PreviousHome string `json:"previous_home_daemon_id,omitempty"`
	Message      string `json:"message"`
}

// Changed reports whether the record on disk moved.
func (r Result) Changed() bool {
	return r.Status == "enrolled" || r.Status == "left"
}

// EnsureDaemonID returns this data dir's durable daemon id, minting it on first
// call. Concurrent daemons agree because the write happens under an flock.
func EnsureDaemonID(dataRoot string) (string, error) {
	if strings.TrimSpace(dataRoot) == "" {
		return "", fmt.Errorf("missing data root")
	}
	if err := os.MkdirAll(dataRoot, 0700); err != nil {
		return "", fmt.Errorf("create data root: %w", err)
	}

	idPath := filepath.Join(dataRoot, DaemonIDFileName)
	unlock, err := lockPath(idPath)
	if err != nil {
		return "", err
	}
	defer unlock()

	if id, err := readDaemonIDFile(idPath); err == nil && id != "" {
		return id, nil
	}

	id, err := newDaemonID()
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(idPath, []byte(id+"\n")); err != nil {
		return "", fmt.Errorf("persist daemon id: %w", err)
	}
	return id, nil
}

// ReadDaemonID returns the daemon id already on disk, or ErrNoDaemonID when
// nothing has ever started in this data dir.
func ReadDaemonID(dataRoot string) (string, error) {
	id, err := readDaemonIDFile(filepath.Join(dataRoot, DaemonIDFileName))
	if err != nil {
		return "", err
	}
	return id, nil
}

// Ensure resolves this daemon's enrollment at startup, writing the home record
// on a fresh install: a daemon nobody enrolled is its own home, and the record
// says so rather than leaving the question open.
func Ensure(dataRoot, daemonID string) (Status, error) {
	if !ValidDaemonID(daemonID) {
		return Status{}, fmt.Errorf("invalid daemon id %q", daemonID)
	}
	recordPath := filepath.Join(dataRoot, RecordFileName)
	unlock, err := lockPath(recordPath)
	if err != nil {
		return Status{}, err
	}
	defer unlock()

	record, err := readRecord(recordPath)
	switch {
	case err == nil:
		return Status{DaemonID: daemonID, HomeDaemonID: record.HomeDaemonID}, nil
	case errors.Is(err, ErrNoRecord):
		if err := writeRecord(recordPath, Record{HomeDaemonID: daemonID, RecordedAt: nowStamp()}); err != nil {
			return Status{}, err
		}
		return Status{DaemonID: daemonID, HomeDaemonID: daemonID}, nil
	default:
		return Status{}, err
	}
}

// Load reads the relationship without writing anything. Callers on the fence
// path use it, so an unreadable record fails the check rather than passing it.
func Load(dataRoot string) (Status, error) {
	daemonID, err := ReadDaemonID(dataRoot)
	if err != nil && !errors.Is(err, ErrNoDaemonID) {
		return Status{}, err
	}
	record, err := readRecord(filepath.Join(dataRoot, RecordFileName))
	if err != nil {
		return Status{DaemonID: daemonID}, err
	}
	return Status{DaemonID: daemonID, HomeDaemonID: record.HomeDaemonID}, nil
}

// Enroll records homeDaemonID as this daemon's home. A daemon that is its own
// home (or has no record yet) enrolls; one already enrolled to the same home is
// unchanged; one enrolled elsewhere refuses with ForeignHomeError.
func Enroll(dataRoot, homeDaemonID string) (Result, error) {
	if !ValidDaemonID(homeDaemonID) {
		return Result{}, fmt.Errorf("invalid home daemon id %q (want d- followed by 32 hex characters)", homeDaemonID)
	}
	daemonID, err := ReadDaemonID(dataRoot)
	if err != nil && !errors.Is(err, ErrNoDaemonID) {
		return Result{}, err
	}
	if daemonID == homeDaemonID {
		return Result{}, fmt.Errorf("a daemon cannot enroll to itself (%s)", homeDaemonID)
	}

	recordPath := filepath.Join(dataRoot, RecordFileName)
	unlock, err := lockPath(recordPath)
	if err != nil {
		return Result{}, err
	}
	defer unlock()

	record, err := readRecord(recordPath)
	if err != nil && !errors.Is(err, ErrNoRecord) {
		return Result{}, err
	}
	current := ""
	if err == nil {
		current = record.HomeDaemonID
	}

	switch current {
	case homeDaemonID:
		return Result{
			Status:       "unchanged",
			DaemonID:     daemonID,
			HomeDaemonID: homeDaemonID,
			Message:      fmt.Sprintf("already an outpost of %s", homeDaemonID),
		}, nil
	case "", daemonID:
		// Never enrolled, or its own home: this is the enrolling act.
	default:
		refusal := &ForeignHomeError{DaemonID: daemonID, CurrentHome: current, RequestedBy: homeDaemonID}
		return Result{
			Status:       "refused",
			DaemonID:     daemonID,
			HomeDaemonID: current,
			PreviousHome: current,
			Message:      refusal.Error(),
		}, refusal
	}

	if err := writeRecord(recordPath, Record{HomeDaemonID: homeDaemonID, RecordedAt: nowStamp()}); err != nil {
		return Result{}, err
	}
	return Result{
		Status:       "enrolled",
		DaemonID:     daemonID,
		HomeDaemonID: homeDaemonID,
		PreviousHome: current,
		Message:      fmt.Sprintf("enrolled as an outpost of %s", homeDaemonID),
	}, nil
}

// Leave is the way out of enrollment: this daemon becomes its own home again,
// which is also what a second home's operator has to do here before that home
// may take it.
func Leave(dataRoot string) (Result, error) {
	daemonID, err := ReadDaemonID(dataRoot)
	if err != nil {
		return Result{}, err
	}

	recordPath := filepath.Join(dataRoot, RecordFileName)
	unlock, err := lockPath(recordPath)
	if err != nil {
		return Result{}, err
	}
	defer unlock()

	record, err := readRecord(recordPath)
	if err != nil && !errors.Is(err, ErrNoRecord) {
		return Result{}, err
	}
	previous := ""
	if err == nil {
		previous = record.HomeDaemonID
	}
	if previous == daemonID {
		return Result{
			Status:       "unchanged",
			DaemonID:     daemonID,
			HomeDaemonID: daemonID,
			Message:      "already a home daemon",
		}, nil
	}
	if err := writeRecord(recordPath, Record{HomeDaemonID: daemonID, RecordedAt: nowStamp()}); err != nil {
		return Result{}, err
	}
	return Result{
		Status:       "left",
		DaemonID:     daemonID,
		HomeDaemonID: daemonID,
		PreviousHome: previous,
		Message:      "now a home daemon; it owns its own garden and crew",
	}, nil
}

// ValidDaemonID reports whether id has the durable `d-<32 hex>` shape.
func ValidDaemonID(id string) bool {
	if !strings.HasPrefix(id, "d-") {
		return false
	}
	if len(id) != 34 {
		return false
	}
	_, err := hex.DecodeString(id[2:])
	return err == nil
}

func newDaemonID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate daemon id: %w", err)
	}
	return "d-" + hex.EncodeToString(buf), nil
}

func readDaemonIDFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNoDaemonID
		}
		return "", fmt.Errorf("read daemon id: %w", err)
	}
	id := strings.TrimSpace(string(data))
	if !ValidDaemonID(id) {
		return "", ErrNoDaemonID
	}
	return id, nil
}

func readRecord(path string) (Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Record{}, ErrNoRecord
		}
		return Record{}, fmt.Errorf("read enrollment record: %w", err)
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("parse enrollment record %s: %w", path, err)
	}
	if !ValidDaemonID(record.HomeDaemonID) {
		return Record{}, fmt.Errorf("enrollment record %s names an invalid home daemon id %q", path, record.HomeDaemonID)
	}
	return record, nil
}

func writeRecord(path string, record Record) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode enrollment record: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create data root: %w", err)
	}
	if err := writeFileAtomic(path, append(data, '\n')); err != nil {
		return fmt.Errorf("persist enrollment record: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	tmpPath := fmt.Sprintf("%s.tmp.%d.%d", path, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// lockPath takes an exclusive flock on <path>.lock and returns the release. The
// lock file is never removed: unlinking it would let a later locker create a
// fresh inode and hold an uncontended lock against a live one.
//
// It creates the data dir first, because a home enrolls a remote that has never
// run a daemon — there is nothing there yet but the binary.
func lockPath(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create data root: %w", err)
	}
	lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open %s lock: %w", filepath.Base(path), err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		lockFile.Close()
		return nil, fmt.Errorf("lock %s: %w", filepath.Base(path), err)
	}
	return func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
	}, nil
}

func nowStamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}
