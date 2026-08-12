// Mines every substantial assistant message that Victor replied to, labelled by
// whether the reply complained about the prose.
//
// The complaint side is what the gate must catch. The silent side is the one
// that decides whether it can ship unasked: firing there is an interruption
// nobody wanted, whatever the prose was secretly like.
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
	// A verdict only counts if a person typed it. Task notifications, teammate
	// relays, and compaction summaries all arrive as user turns and all carry
	// words like "concise" that have nothing to do with the message before them.
	Origin struct {
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

// The same patterns that mined the paired corpus, so the two label the same way.
var complaint = regexp.MustCompile(`(?i)\b(plain(ly)?|plain english|plain language|plain words|in english|simple|simpler|simplest|simplify|simplified|dumb it down|eli5|explain like|loaded|jargon|buzzword|marketing|salesy|corporate|fluff|slop|dense|density|wordy|verbose|convoluted|impenetrable|unreadable|wall of text|rambl\w*|concise|concisely|shorter|trim|tighten|cut the|less prose|fewer words|be brief|briefer|clearer|clarity|unclear)\b` +
	`|\bhard to (read|understand|follow|parse|grok)\b|\bcan'?t (read|follow|understand|parse)\b|\bdifficult to (read|understand|follow)\b` +
	`|\btoo (long|much|many words|wordy|dense|complex|complicated|abstract|clever|verbose)\b` +
	`|\bwhat (does|do) (that|this|you) mean\b|\bi don'?t (understand|get) (what|this|that|you)\b|\bmakes no sense\b|\bgibberish\b` +
	`|\b(rewrite|re-?word|rephrase|say it again)\b`)

// Roughly the gate's floor. Exact word counting happens in-package against the
// real tokenizer; this only keeps the file from holding what cannot be judged.
const minRawWords = 120

type record struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	CWD       string `json:"cwd"`
	Timestamp string `json:"timestamp"`
	Label     string `json:"label"` // "complaint" or "silent"
	Reply     string `json:"reply"` // the user message that followed
	Text      string `json:"text"`  // the assistant message being judged
}

func main() {
	root := os.Args[1]
	out := json.NewEncoder(os.Stdout)
	var files, kept, complaints int

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
				if len(strings.Fields(text)) < minRawWords {
					continue
				}
				// Only the last substantial message before a reply is judged;
				// the reply is a verdict on what the user just read.
				pending = &record{File: path, Line: line, CWD: e.CWD, Timestamp: e.Timestamp, Text: text}
			case "user":
				if e.Origin.Kind != "human" || e.PromptSource != "typed" {
					continue
				}
				reply := clean(textOf(e.Message))
				if reply == "" || pending == nil {
					continue
				}
				pending.Reply = reply
				if len(pending.Reply) > 600 {
					pending.Reply = pending.Reply[:600]
				}
				pending.Label = "silent"
				if complaint.MatchString(reply) {
					pending.Label = "complaint"
					complaints++
				}
				kept++
				out.Encode(pending)
				pending = nil
			}
		}
		return nil
	})
	fmt.Fprintf(os.Stderr, "files=%d judged=%d complaints=%d silent=%d\n",
		files, kept, complaints, kept-complaints)
}
