package automode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The durable local record a pi session writes for every refused tool call,
// before it tries to report the denial. The report can be lost; this cannot.
// The writer is plugins/attn-pi/automode/ledger.ts. Design:
// docs/plans/2026-08-18-automode-denial-ledger.md.

// DenialLedgerFileName is the ledger's basename in either data dir.
const DenialLedgerFileName = "attn-automode-denials.jsonl"

// DenialLedgerEnvVar names the file for the plugin runtime, which forwards it.
const DenialLedgerEnvVar = "ATTN_AUTOMODE_DENIAL_LOG"

// DenialLedgerPath is where a daemon with this data dir tells sessions to write.
func DenialLedgerPath(dataDir string) string {
	return filepath.Join(dataDir, DenialLedgerFileName)
}

// DenialLedgerRecord is one refused call as the session wrote it.
type DenialLedgerRecord struct {
	SessionID  string    `json:"session_id"`
	ToolCallID string    `json:"tool_call_id"`
	Tool       string    `json:"tool"`
	Action     string    `json:"action"`
	Reason     string    `json:"reason"`
	Rule       string    `json:"rule"`
	At         time.Time `json:"-"`
	// Clearable is false when the user's approval could not have lifted this
	// block. Absent from the line for the ordinary, arguable denial.
	Clearable *bool `json:"clearable,omitempty"`
	// What the deciding layer was sent, for a call a classifier judged. Read
	// but never reconciled into the store: it is a page, not a row.
	Prompt *DenialLedgerPrompt `json:"prompt,omitempty"`
}

// DenialLedgerPrompt is one classification's exact input.
type DenialLedgerPrompt struct {
	Layer  string `json:"layer"`
	System string `json:"system"`
	User   string `json:"user"`
}

// DenialLedgerReading is what one read found. Dropped and Malformed are what
// the file admits it lost; without them a partial episode reads as a whole one.
type DenialLedgerReading struct {
	Records   []DenialLedgerRecord
	Dropped   int
	Malformed int
}

// ReadDenialLedger reads a ledger and its one rotated generation, oldest first.
// A missing file is not an error.
func ReadDenialLedger(path string) (DenialLedgerReading, error) {
	reading := DenialLedgerReading{}
	// Oldest generation first, so the returned records are in the order they
	// were written across the rotation boundary.
	for _, generation := range []string{path + ".1", path} {
		if err := readDenialGeneration(generation, &reading); err != nil {
			return reading, err
		}
	}
	sort.SliceStable(reading.Records, func(i, j int) bool {
		return reading.Records[i].At.Before(reading.Records[j].At)
	})
	return reading, nil
}

func readDenialGeneration(path string, into *DenialLedgerReading) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read denial ledger %s: %w", path, err)
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 64*1024)
	for {
		bytesRead, tooLong, err := readLedgerLine(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read denial ledger %s: %w", path, err)
		}
		// One line past the cap is one denial lost, and the lines after it are
		// not: the reader steps over it, counts it, and keeps going.
		if tooLong {
			into.Malformed++
			continue
		}
		line := strings.TrimSpace(string(bytesRead))
		if line == "" {
			continue
		}
		var raw denialLedgerLine
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			into.Malformed++
			continue
		}
		if raw.Type == "rotated" {
			into.Dropped += raw.Dropped
			continue
		}
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(raw.At))
		if err != nil {
			into.Malformed++
			continue
		}
		into.Records = append(into.Records, DenialLedgerRecord{
			SessionID:  strings.TrimSpace(raw.SessionID),
			ToolCallID: strings.TrimSpace(raw.ToolCallID),
			Tool:       strings.TrimSpace(raw.Tool),
			Action:     strings.TrimSpace(raw.Action),
			Reason:     strings.TrimSpace(raw.Reason),
			Rule:       strings.TrimSpace(raw.Rule),
			At:         at,
			Clearable:  raw.Clearable,
			Prompt:     raw.Prompt,
		})
	}
	return nil
}

// readLedgerLine returns one line without its newline. A line past
// denialLedgerMaxLineBytes is skipped and reported rather than stopping the
// read, which would silently drop every record after it.
func readLedgerLine(reader *bufio.Reader) (line []byte, tooLong bool, err error) {
	for {
		chunk, isPrefix, err := reader.ReadLine()
		if err != nil {
			return nil, false, err
		}
		line = append(line, chunk...)
		if !isPrefix {
			return line, tooLong, nil
		}
		if len(line) > denialLedgerMaxLineBytes {
			// Keep reading to the newline so the next line starts where it
			// should, but hold nothing: the point is to get past this one.
			line = line[:0]
			tooLong = true
		}
	}
}

// denialLedgerMaxLineBytes bounds one record in memory. The classifier prompt
// is the largest field: measured 2026-08-22, the transcript budgets cap a line
// at 24,569 bytes, so 1 MiB is 40x past it.
const denialLedgerMaxLineBytes = 1024 * 1024

type denialLedgerLine struct {
	Type       string              `json:"type"`
	Dropped    int                 `json:"dropped"`
	SessionID  string              `json:"session_id"`
	ToolCallID string              `json:"tool_call_id"`
	Tool       string              `json:"tool"`
	Action     string              `json:"action"`
	Reason     string              `json:"reason"`
	Rule       string              `json:"rule"`
	At         string              `json:"at"`
	Clearable  *bool               `json:"clearable"`
	Prompt     *DenialLedgerPrompt `json:"prompt"`
}
