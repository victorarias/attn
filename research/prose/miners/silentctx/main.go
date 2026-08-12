// Like mine/silent, but each judged assistant message carries the
// user-visible conversation that preceded it: prior human-typed turns and
// prior assistant text. Tool results and machine-injected user turns are the
// agent's knowledge, not the reader's, so they stay out — the point is to
// reconstruct what the reader had seen when the message landed.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type entry struct {
	Type        string          `json:"type"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	CWD         string          `json:"cwd"`
	Timestamp   string          `json:"timestamp"`
	Message     json.RawMessage `json:"message"`
	Origin      struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	PromptSource string `json:"promptSource"`
}

func textOf(raw json.RawMessage) string {
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(m.Content, &blocks) != nil {
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

const minRawWords = 120

type turn struct {
	Role string `json:"role"` // "user" or "agent"
	Text string `json:"text"`
}

type record struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	CWD       string `json:"cwd"`
	Timestamp string `json:"timestamp"`
	Reply     string `json:"reply"`
	Text      string `json:"text"`
	Context   []turn `json:"context"` // visible turns before Text, oldest first
}

// Keep the tail of the window under a char budget so the judge prompt stays
// bounded; 16k chars is roughly 8 substantial turns.
func capContext(w []turn) []turn {
	const budget = 16000
	total := 0
	for i := len(w) - 1; i >= 0; i-- {
		total += len(w[i].Text)
		if total > budget {
			return append([]turn{}, w[i+1:]...)
		}
	}
	return append([]turn{}, w...)
}

func main() {
	root := os.Args[1]
	out := json.NewEncoder(os.Stdout)
	var files, kept int

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		files++

		var window []turn
		var pending *record
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
		for line := 1; sc.Scan(); line++ {
			var e entry
			if json.Unmarshal(sc.Bytes(), &e) != nil || e.IsSidechain || e.IsMeta {
				continue
			}
			switch e.Type {
			case "assistant":
				text := clean(textOf(e.Message))
				if text == "" {
					continue
				}
				if len(strings.Fields(text)) >= minRawWords {
					pending = &record{File: path, Line: line, CWD: e.CWD,
						Timestamp: e.Timestamp, Text: text, Context: capContext(window)}
				}
				window = append(window, turn{Role: "agent", Text: text})
				if len(window) > 40 {
					window = window[len(window)-40:]
				}
			case "user":
				if e.Origin.Kind != "human" || e.PromptSource != "typed" {
					continue
				}
				reply := clean(textOf(e.Message))
				if reply == "" {
					continue
				}
				if pending != nil {
					pending.Reply = reply
					if len(pending.Reply) > 600 {
						pending.Reply = pending.Reply[:600]
					}
					kept++
					out.Encode(pending)
					pending = nil
				}
				window = append(window, turn{Role: "user", Text: reply})
				if len(window) > 40 {
					window = window[len(window)-40:]
				}
			}
		}
		return nil
	})
	fmt.Fprintf(os.Stderr, "files=%d judged=%d\n", files, kept)
}
