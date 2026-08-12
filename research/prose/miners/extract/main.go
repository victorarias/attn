// Extracts the full window around each curated complaint: the dense assistant
// message, Victor's complaint, and the revision his complaint produced. The
// revision is the control arm — what good feedback already achieves.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

type entry struct {
	Type        string          `json:"type"`
	UUID        string          `json:"uuid"`
	Timestamp   string          `json:"timestamp"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	Message     json.RawMessage `json:"message"`
}

type message struct {
	Content json.RawMessage `json:"content"`
}

type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func textOf(raw json.RawMessage) string {
	var m message
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return s
	}
	var blocks []block
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

var noise = regexp.MustCompile(`(?is)<(system-reminder|command-name|command-message|command-args|local-command-stdout|user-prompt-submit-hook)>.*?</[a-z-]+>`)

func clean(s string) string { return strings.TrimSpace(noise.ReplaceAllString(s, "")) }

type hit struct {
	File     string   `json:"file"`
	Line     int      `json:"line"`
	PrevLine int      `json:"prev_line"`
	Tags     []string `json:"tags"`
	UserText string   `json:"user_text"`
	Ts       string   `json:"timestamp"`
	CWD      string   `json:"cwd"`
}

type window struct {
	Index      int      `json:"index"`
	File       string   `json:"file"`
	SessionID  string   `json:"session_id"`
	CWD        string   `json:"cwd"`
	Timestamp  string   `json:"timestamp"`
	Tags       []string `json:"tags"`
	DenseLine  int      `json:"dense_line"`
	Dense      string   `json:"dense"`      // the prose Victor objected to
	Complaint  string   `json:"complaint"`  // his words — the label
	Revision   string   `json:"revision"`   // what his complaint produced — control arm
	TurnsBefore int     `json:"turns_before"` // conversation depth at that point
}

func main() {
	hitsPath, idxCSV := os.Args[1], os.Args[2]
	want := map[int]bool{}
	for _, s := range strings.Split(idxCSV, ",") {
		var n int
		fmt.Sscanf(strings.TrimSpace(s), "%d", &n)
		want[n] = true
	}

	hf, err := os.Open(hitsPath)
	if err != nil {
		panic(err)
	}
	defer hf.Close()
	var hits []hit
	sc := bufio.NewScanner(hf)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		var h hit
		if err := json.Unmarshal(sc.Bytes(), &h); err == nil {
			hits = append(hits, h)
		}
	}

	out := json.NewEncoder(os.Stdout)
	for i, h := range hits {
		if !want[i] {
			continue
		}
		f, err := os.Open(h.File)
		if err != nil {
			continue
		}
		w := window{
			Index: i, File: h.File, CWD: h.CWD, Timestamp: h.Ts, Tags: h.Tags,
			DenseLine: h.PrevLine, Complaint: h.UserText,
		}
		parts := strings.Split(h.File, "/")
		w.SessionID = strings.TrimSuffix(parts[len(parts)-1], ".jsonl")

		line, turns := 0, 0
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
		for sc.Scan() {
			line++
			var e entry
			if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
				continue
			}
			if e.IsSidechain {
				continue
			}
			if line < h.Line && (e.Type == "user" || e.Type == "assistant") {
				turns++
			}
			if line == h.PrevLine {
				w.Dense = clean(textOf(e.Message))
			}
			// The revision is the first substantive prose after the complaint.
			// A length floor skips tool-call preambles ("Let me check…") without
			// drifting to a later, unrelated message the way "longest" does.
			if line > h.Line && e.Type == "assistant" && w.Revision == "" && line <= h.Line+80 {
				if t := clean(textOf(e.Message)); len(t) >= 400 {
					w.Revision = t
				}
			}
		}
		f.Close()
		w.TurnsBefore = turns
		out.Encode(w)
	}
}
