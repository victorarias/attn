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

const DenialLedgerFileName = "attn-automode-denials.jsonl"

const DenialLedgerEnvVar = "ATTN_AUTOMODE_DENIAL_LOG"

func DenialLedgerPath(dataDir string) string {
	return filepath.Join(dataDir, DenialLedgerFileName)
}

type DenialLedgerRecord struct {
	SessionID  string    `json:"session_id"`
	ToolCallID string    `json:"tool_call_id"`
	Tool       string    `json:"tool"`
	Action     string    `json:"action"`
	Reason     string    `json:"reason"`
	Rule       string    `json:"rule"`
	At         time.Time `json:"-"`

	Clearable *bool `json:"clearable,omitempty"`

	Prompt *DenialLedgerPrompt `json:"prompt,omitempty"`
}

type DenialLedgerPrompt struct {
	Layer  string `json:"layer"`
	System string `json:"system"`
	User   string `json:"user"`
}

type DenialLedgerReading struct {
	Records   []DenialLedgerRecord
	Dropped   int
	Malformed int
}

func ReadDenialLedger(path string) (DenialLedgerReading, error) {
	reading := DenialLedgerReading{}

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

			line = line[:0]
			tooLong = true
		}
	}
}

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
