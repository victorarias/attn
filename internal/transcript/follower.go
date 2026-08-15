package transcript

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
)

// FollowRecord is one complete source record and the provider-neutral events
// decoded from it. Raw stays available because live state classification reads
// provider-specific evidence that is intentionally not part of Event.
type FollowRecord struct {
	Raw    []byte
	Events []Event
	Usage  *TokenUsage
}

// FollowBatch is the append-only delta since the follower's previous read.
type FollowBatch struct {
	Records []FollowRecord
	Events  []Event
	Usage   []TokenUsage
}

// Follower reads each complete transcript record once while preserving the
// canonical event cursor and provider echo-deduplication semantics.
type Follower struct {
	path             string
	agent            string
	offset           int64
	fingerprint      string
	previousEvent    Event
	hasPreviousEvent bool
	usage            *UsageExtractor
}

// NewFollower starts at startOffset. A non-zero offset may point into a record;
// the incomplete prefix is consumed as provider noise, matching the watcher's
// existing bounded-bootstrap behavior.
func NewFollower(path, agent string, startOffset int64) (*Follower, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if startOffset < 0 {
		startOffset = 0
	}
	if startOffset > info.Size() {
		startOffset = info.Size()
	}
	fingerprint, _, err := transcriptFingerprint(f)
	if err != nil {
		return nil, err
	}
	previous, hasPrevious, err := previousNormalizedEvent(f, agent, startOffset)
	if err != nil {
		return nil, err
	}
	usage := NewUsageExtractor(agent)
	if err := usage.seedCodexModelBefore(f, startOffset); err != nil {
		return nil, err
	}
	return &Follower{
		path:             path,
		agent:            agent,
		offset:           startOffset,
		fingerprint:      fingerprint,
		previousEvent:    previous,
		hasPreviousEvent: hasPrevious,
		usage:            usage,
	}, nil
}

// NewFollowerAfterCursor resumes after the last complete record read by a
// prior Follower. Unlike a bare byte offset, cursor also binds the checkpoint
// to the transcript's first record, so rotation or replacement fails loudly.
func NewFollowerAfterCursor(path, agent, cursor string) (*Follower, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return NewFollower(path, agent, 0)
	}
	expectedFingerprint, offset, eventIndex, err := decodeEventCursor(cursor)
	if err != nil || eventIndex != 0 {
		return nil, ErrInvalidCursor
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if offset > info.Size() {
		return nil, ErrCursorPastEnd
	}
	fingerprint, hasCompleteRecord, err := transcriptFingerprint(file)
	if err != nil {
		return nil, err
	}
	if !hasCompleteRecord || fingerprint != expectedFingerprint {
		return nil, ErrCursorMismatch
	}
	if offset > 0 {
		var previous [1]byte
		if _, err := file.ReadAt(previous[:], offset-1); err != nil || previous[0] != '\n' {
			return nil, ErrInvalidCursor
		}
	}
	return NewFollower(path, agent, offset)
}

// Cursor returns the durable checkpoint after the last complete record read.
// A partially written final record is not included.
func (f *Follower) Cursor() string {
	if f == nil || f.fingerprint == "" {
		return ""
	}
	return encodeEventCursor(f.fingerprint, f.offset, 0)
}

// Read returns complete records appended since the previous call. A partial
// final record is left unread until its newline arrives.
func (f *Follower) Read() (FollowBatch, error) {
	file, err := os.Open(f.path)
	if err != nil {
		return FollowBatch{}, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return FollowBatch{}, err
	}
	if info.Size() < f.offset {
		return FollowBatch{}, ErrCursorPastEnd
	}
	fingerprint, hasCompleteRecord, err := transcriptFingerprint(file)
	if err != nil {
		return FollowBatch{}, err
	}
	if hasCompleteRecord {
		if f.fingerprint != "" && f.fingerprint != fingerprint {
			return FollowBatch{}, ErrCursorMismatch
		}
		f.fingerprint = fingerprint
	}
	if _, err := file.Seek(f.offset, io.SeekStart); err != nil {
		return FollowBatch{}, err
	}

	batch := FollowBatch{}
	reader := bufio.NewReader(file)
	for {
		record, readErr := reader.ReadBytes('\n')
		if len(record) == 0 || record[len(record)-1] != '\n' {
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return FollowBatch{}, readErr
			}
			return batch, nil
		}

		lineOffset := f.offset
		f.offset += int64(len(record))
		raw := bytes.TrimSpace(record)
		allEvents, previousEvent, hasPreviousEvent := decodeEventRecord(
			f.agent,
			raw,
			f.previousEvent,
			f.hasPreviousEvent,
		)
		events := make([]Event, len(allEvents))
		copy(events, allEvents)
		for i := range events {
			events[i].Cursor = encodeEventCursor(f.fingerprint, lineOffset, i+1)
		}
		f.previousEvent = previousEvent
		f.hasPreviousEvent = hasPreviousEvent
		followRecord := FollowRecord{Raw: append([]byte(nil), raw...), Events: events}
		if usage, ok := f.usage.Observe(raw, encodeEventCursor(f.fingerprint, lineOffset, 0)); ok {
			followRecord.Usage = &usage
			batch.Usage = append(batch.Usage, usage)
		}
		batch.Records = append(batch.Records, followRecord)
		batch.Events = append(batch.Events, events...)
	}
}
