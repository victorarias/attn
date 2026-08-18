package automode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The denial ledger is the durable local record a pi session writes for every
// tool call auto mode refuses, before it tries to report the denial to the
// daemon. The report can be lost — a bare pi has no relay, and a relay whose
// socket died drops what it is handed — so this file is the only thing that
// makes a denial readable afterwards.
//
// The writer is plugins/attn-pi/automode/ledger.ts; the two file names and the
// record shape below are that file's. Design:
// docs/plans/2026-08-18-automode-denial-ledger.md.

// DenialLedgerFileName is the ledger's basename, in attn's data dir and in
// pi's agent dir alike.
const DenialLedgerFileName = "attn-automode-denials.jsonl"

// DenialLedgerEnvVar names the file for the plugin runtime, which forwards it
// to the pi session it spawns.
const DenialLedgerEnvVar = "ATTN_AUTOMODE_DENIAL_LOG"

// DenialLedgerPath is where a daemon with this data dir tells its sessions to
// write.
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
}

// DenialLedgerReading is what one read of a ledger found. Dropped and
// Malformed are what the file admits it lost: a reader that is shown the
// records without them is being told a partial episode is a whole one.
type DenialLedgerReading struct {
	Records   []DenialLedgerRecord
	Dropped   int
	Malformed int
}

// ReadDenialLedger reads a ledger and the one rotated generation beside it,
// oldest record first. A missing file is not an error: a machine where auto
// mode never refused anything has no ledger to read.
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

	scanner := bufio.NewScanner(file)
	// A denial's action and reason are prose the model was shown, so a line is
	// far larger than bufio's 64KB default allows for.
	scanner.Buffer(make([]byte, 0, 64*1024), denialLedgerMaxLineBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
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
		})
	}
	if err := scanner.Err(); err != nil {
		// A line too long to read is one denial lost, not a failed read: the
		// rest of the file is still the record everything else depends on.
		into.Malformed++
		return nil
	}
	return nil
}

// denialLedgerMaxLineBytes bounds one record. Measured 2026-08-18: a real
// denial line is ~450 bytes, and the largest field is the classifier's reason,
// which the pi side already writes as one collapsed line. 1 MiB is past any
// denial and short of a file that would be read into memory by accident.
const denialLedgerMaxLineBytes = 1024 * 1024

type denialLedgerLine struct {
	Type       string `json:"type"`
	Dropped    int    `json:"dropped"`
	SessionID  string `json:"session_id"`
	ToolCallID string `json:"tool_call_id"`
	Tool       string `json:"tool"`
	Action     string `json:"action"`
	Reason     string `json:"reason"`
	Rule       string `json:"rule"`
	At         string `json:"at"`
}
