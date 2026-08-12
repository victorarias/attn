package prosegate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Can a cheap model predict what six surface features could not: whether this
// message draws a complaint about how it is written?
const judgePrompt = `You will be shown a message an AI coding agent wrote to the engineer it works for.

That engineer is blunt and dislikes dense, jargon-heavy, over-hedged writing. Sometimes he replies asking for it again in plain language; usually he just gets on with the work.

Rate from 0 to 10 how likely he is to push back on how this message is WRITTEN — not on whether he agrees with it, and not on whether the work is good.

0 means he will not comment on the writing at all. 10 means he will certainly ask for it in plainer words.

Answer with a single integer and nothing else.`

type judgeRow struct {
	Model string  `json:"model"`
	Label string  `json:"label"`
	Score float64 `json:"score"`
	Err   string  `json:"error,omitempty"`
}

var digits = regexp.MustCompile(`\d+`)

func askHaiku(text string) (float64, error) {
	cmd := exec.Command("claude", "-p", "--strict-mcp-config",
		"--model", "claude-haiku-4-5-20251001", "--max-turns", "1",
		"--system-prompt", judgePrompt)
	cmd.Stdin = strings.NewReader(text)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	return parseScore(string(out))
}

func askLuna(text string) (float64, error) {
	cmd := exec.Command("codex", "exec", "-m", "gpt-5.6-luna", "--skip-git-repo-check", "-")
	cmd.Dir = os.TempDir()
	cmd.Stdin = strings.NewReader(judgePrompt + "\n\n---\n\n" + text)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	// codex prints a run log; the answer is the last non-empty line.
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return parseScore(s)
		}
	}
	return 0, fmt.Errorf("no output")
}

func parseScore(s string) (float64, error) {
	m := digits.FindString(strings.TrimSpace(s))
	if m == "" {
		return 0, fmt.Errorf("no digit in %q", truncate(s, 60))
	}
	n, err := strconv.Atoi(m)
	return float64(n), err
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func TestJudgeExperiment(t *testing.T) {
	if os.Getenv("ATTN_JUDGE_RUN") == "" {
		t.Skip("set ATTN_JUDGE_RUN=1")
	}
	recs := loadSilent(t)

	var complaints, silent []silentRec
	for _, r := range recs {
		if _, w := Measure(r.Text); w < MinWords {
			continue
		}
		if r.Label == "complaint" {
			complaints = append(complaints, r)
		} else {
			silent = append(silent, r)
		}
	}
	rng := rand.New(rand.NewSource(7))
	rng.Shuffle(len(silent), func(i, j int) { silent[i], silent[j] = silent[j], silent[i] })
	if len(silent) > 350 {
		silent = silent[:350]
	}
	sample := append(append([]silentRec{}, complaints...), silent...)
	t.Logf("judging %d messages (%d complaint, %d silent) x 2 models",
		len(sample), len(complaints), len(silent))

	f, err := os.Create(os.Getenv(harnessEnv) + "/judge.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()

	var mu sync.Mutex
	rows := map[string][]judgeRow{}
	emit := func(r judgeRow) {
		mu.Lock()
		defer mu.Unlock()
		rows[r.Model] = append(rows[r.Model], r)
		b, _ := json.Marshal(r)
		w.Write(append(b, '\n'))
	}

	models := map[string]func(string) (float64, error){"haiku": askHaiku, "luna": askLuna}
	var wg sync.WaitGroup
	start := time.Now()
	for name, ask := range models {
		slots := make(chan struct{}, 8)
		for _, rec := range sample {
			wg.Add(1)
			go func(name string, ask func(string) (float64, error), rec silentRec) {
				defer wg.Done()
				slots <- struct{}{}
				defer func() { <-slots }()
				score, err := ask(rec.Text)
				row := judgeRow{Model: name, Label: rec.Label, Score: score}
				if err != nil {
					row.Err = truncate(err.Error(), 120)
				}
				emit(row)
			}(name, ask, rec)
		}
	}
	wg.Wait()
	t.Logf("done in %s", time.Since(start).Round(time.Second))

	var names []string
	for n := range rows {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		var pos, neg []float64
		var failed int
		for _, r := range rows[name] {
			if r.Err != "" {
				failed++
				continue
			}
			if r.Label == "complaint" {
				pos = append(pos, r.Score)
			} else {
				neg = append(neg, r.Score)
			}
		}
		mean := func(xs []float64) float64 {
			s := 0.0
			for _, x := range xs {
				s += x
			}
			return s / float64(max(len(xs), 1))
		}
		t.Logf("%-6s AUC %.3f   mean complaint %.2f   mean silent %.2f   (n=%d/%d, %d failed)",
			name, auc(pos, neg), mean(pos), mean(neg), len(pos), len(neg), failed)

		// What an operating point would cost: fire at or above each score.
		for _, cut := range []float64{5, 6, 7, 8} {
			over := func(xs []float64) float64 {
				n := 0
				for _, x := range xs {
					if x >= cut {
						n++
					}
				}
				return 100 * float64(n) / float64(max(len(xs), 1))
			}
			t.Logf("   >=%.0f  fires on %5.1f%% of silent, %5.1f%% of complaints", cut, over(neg), over(pos))
		}
	}
}
