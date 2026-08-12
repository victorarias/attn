package prosegate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// The harness replays real complaints. For each case it rebuilds the
// conversation up to the message Victor objected to, answers in his place with
// a nudge, and measures what the agent writes back.
//
// Three arms:
//
//	real       the revision Victor actually accepted — the target, free
//	generic    "please simplify" — the control, so the mechanism has to beat
//	           the cheapest thing anyone would have typed
//	mechanism  the nudge this package ships
//
// It costs money and minutes, so it runs only when asked:
//
//	ATTN_PROSE_CORPUS=/path/corpus_pure.jsonl \
//	ATTN_PROSE_HARNESS=/path/out go test ./internal/prosegate -run TestNudgeHarness -v -timeout 30m
const (
	harnessEnv   = "ATTN_PROSE_HARNESS"
	genericNudge = "Please simplify."
	harnessModel = "opus"

	contextTurns = 6    // conversation turns rebuilt before the dense message
	contextUsers = 2    // ... widened until this many of them are the user's
	contextMax   = 16   // ... but never past here
	turnBudget   = 1500 // characters kept per rebuilt turn
	harnessJobs  = 4
)

type harnessCase struct {
	corpusCase
	File      string `json:"file"`
	DenseLine int    `json:"dense_line"`
	Complaint string `json:"complaint"`
}

type armResult struct {
	Index int    `json:"index"`
	Arm   string `json:"arm"`
	Text  string `json:"text"`
	Words int    `json:"words"`
	// Tripped is pass/fail against the shipped thresholds. Improved is the
	// paired question — did this rewrite move the feature down from the message
	// it replaced — and it is the one that means something, because the "real"
	// arm is the accepted side of the corpus that set those thresholds and can
	// only ever score zero trips.
	Tripped  bool            `json:"tripped"`
	Abstain  bool            `json:"abstained"`
	Gates    []string        `json:"gates,omitempty"`
	Improved map[string]bool `json:"improved,omitempty"`
	Lost     []string        `json:"lost,omitempty"`
	Duration string          `json:"duration,omitempty"`
	Err      string          `json:"error,omitempty"`
}

func TestNudgeHarness(t *testing.T) {
	corpusPath, outDir := os.Getenv(corpusEnv), os.Getenv(harnessEnv)
	if corpusPath == "" || outDir == "" {
		t.Skipf("set %s and %s to run the replay harness", corpusEnv, harnessEnv)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("output dir: %v", err)
	}
	cases := loadHarnessCases(t, corpusPath)
	t.Logf("replaying %d cases x 2 model arms against %s", len(cases), harnessModel)

	var (
		mu      sync.Mutex
		results []armResult
		wg      sync.WaitGroup
		slots   = make(chan struct{}, harnessJobs)
	)
	record := func(r armResult) {
		mu.Lock()
		results = append(results, r)
		mu.Unlock()
	}

	for _, c := range cases {
		record(measureArm(c.Index, "real", c.Dense, c.Revision, 0, nil))

		history, err := rebuildContext(c.File, c.DenseLine)
		if err != nil {
			t.Errorf("case %d: rebuild context: %v", c.Index, err)
			continue
		}
		for _, arm := range []struct{ name, nudge string }{
			{"generic", genericNudge},
			{"mechanism", DefaultNudge},
		} {
			wg.Add(1)
			go func(c harnessCase, history, name, nudge string) {
				defer wg.Done()
				slots <- struct{}{}
				defer func() { <-slots }()

				start := time.Now()
				text, err := askForRewrite(history, c.Dense, nudge)
				record(measureArm(c.Index, name, c.Dense, text, time.Since(start), err))
			}(c, history, arm.name, arm.nudge)
		}
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool {
		if results[i].Index != results[j].Index {
			return results[i].Index < results[j].Index
		}
		return results[i].Arm < results[j].Arm
	})
	writeHarnessReport(t, filepath.Join(outDir, "harness.jsonl"), results)
	reportArms(t, results)
}

// measureArm judges one rewrite: does it still trip, and did it drop anything
// the original carried.
func measureArm(index int, arm, dense, text string, took time.Duration, err error) armResult {
	r := armResult{Index: index, Arm: arm, Text: text}
	if took > 0 {
		r.Duration = took.Round(time.Millisecond).String()
	}
	if err != nil {
		r.Err = err.Error()
		return r
	}
	v := Check(text, Default())
	r.Words, r.Tripped, r.Abstain = v.Words, v.Tripped, v.Abstained
	for _, g := range v.Gates {
		r.Gates = append(r.Gates, g.Name)
	}
	r.Lost = StructureOf(dense).Lost(StructureOf(text))

	before, _ := Measure(dense)
	after, _ := Measure(text)
	r.Improved = map[string]bool{}
	for feature, was := range before {
		if now, ok := after[feature]; ok {
			r.Improved[feature] = now < was
		}
	}
	return r
}

var armOrder = []string{"real", "generic", "mechanism"}

func reportArms(t *testing.T, results []armResult) {
	type tally struct{ n, judged, tripped, lost, failed, words int }
	byArm := map[string]*tally{}
	for _, r := range results {
		a, ok := byArm[r.Arm]
		if !ok {
			a = &tally{}
			byArm[r.Arm] = a
		}
		if r.Err != "" {
			a.failed++
			continue
		}
		a.n++
		a.words += r.Words
		if len(r.Lost) > 0 {
			a.lost++
		}
		// A rewrite under the word floor abstains. Counting it as "did not trip"
		// would score an arm for going short rather than for going plain.
		if r.Abstain {
			continue
		}
		a.judged++
		if r.Tripped {
			a.tripped++
		}
	}
	t.Log("arm         n   judged   still trips   dropped structure   mean words")
	for _, name := range armOrder {
		a, ok := byArm[name]
		if !ok || a.n == 0 {
			continue
		}
		t.Logf("%-10s %2d    %2d      %2d (%3.0f%%)     %2d (%3.0f%%)           %4d",
			name, a.n, a.judged,
			a.tripped, float64(a.tripped)/float64(max(a.judged, 1))*100,
			a.lost, float64(a.lost)/float64(a.n)*100,
			a.words/a.n)
		if a.failed > 0 {
			t.Logf("%-10s %d call(s) failed", name, a.failed)
		}
	}
	reportPairedImprovement(t, results)
	// A silent arm is a broken harness, not a perfect nudge.
	for _, name := range []string{"generic", "mechanism"} {
		if a := byArm[name]; a == nil || a.n == 0 {
			t.Errorf("arm %q produced no measurable output", name)
		}
	}
}

// reportPairedImprovement asks the only question the "real" arm can answer:
// against the message it replaced, did this rewrite move each feature down?
func reportPairedImprovement(t *testing.T, results []armResult) {
	type key struct{ arm, feature string }
	better, total := map[key]int{}, map[key]int{}
	for _, r := range results {
		for feature, improved := range r.Improved {
			total[key{r.Arm, feature}]++
			if improved {
				better[key{r.Arm, feature}]++
			}
		}
	}
	features := make([]string, 0, len(DefaultThresholds()))
	for f := range DefaultThresholds() {
		features = append(features, f)
	}
	sort.Strings(features)

	t.Log("moved down against the message it replaced:")
	t.Logf("%-20s %12s %12s %12s", "feature", armOrder[0], armOrder[1], armOrder[2])
	for _, feature := range features {
		row := fmt.Sprintf("%-20s", feature)
		for _, arm := range armOrder {
			k := key{arm, feature}
			row += fmt.Sprintf(" %5d/%-2d %3.0f%%", better[k], total[k],
				float64(better[k])/float64(max(total[k], 1))*100)
		}
		t.Log(row)
	}
}

func loadHarnessCases(t *testing.T, path string) []harnessCase {
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open corpus: %v", err)
	}
	defer f.Close()

	var cases []harnessCase
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		var c harnessCase
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatalf("corpus line: %v", err)
		}
		// Below the floor the gate abstains, so the case cannot be scored.
		if _, words := Measure(c.Dense); words < MinWords {
			continue
		}
		cases = append(cases, c)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("no judgeable cases in corpus")
	}
	return cases
}

func writeHarnessReport(t *testing.T, path string, results []armResult) {
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("write report: %v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range results {
		if err := enc.Encode(r); err != nil {
			t.Fatalf("encode report: %v", err)
		}
	}
	t.Logf("report: %s", path)
}

// --- reconstruction -------------------------------------------------------

type transcriptEntry struct {
	Type        string          `json:"type"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	Message     json.RawMessage `json:"message"`
}

// rebuildContext replays the turns leading up to the dense message. A full
// resume would drag in every tool result and blow the prompt open; the last few
// turns of plain text are what a reader of the complaint had in mind.
func rebuildContext(path string, denseLine int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	type turn struct {
		speaker string
		text    string
	}
	var turns []turn
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for line := 1; sc.Scan(); line++ {
		if line >= denseLine {
			break // the dense message is presented separately
		}
		var e transcriptEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil || e.IsSidechain || e.IsMeta {
			continue
		}
		var speaker string
		switch e.Type {
		case "user":
			speaker = "User"
		case "assistant":
			speaker = "Assistant"
		default:
			continue
		}
		text := cleanTranscriptText(transcriptText(e.Message))
		if text == "" {
			continue
		}
		if len(text) > turnBudget {
			text = text[:turnBudget] + " […]"
		}
		turns = append(turns, turn{speaker, text})
	}
	if err := sc.Err(); err != nil {
		return "", err
	}

	// A window of the last few messages is mostly the agent talking — its turns
	// arrive split around tool calls. Widen it until the questions being answered
	// are inside it, because those are what the complaint was about.
	start := len(turns)
	users := 0
	for start > 0 && len(turns)-start < contextMax {
		if len(turns)-start >= contextTurns && users >= contextUsers {
			break
		}
		start--
		if turns[start].speaker == "User" {
			users++
		}
	}

	var lines []string
	for _, t := range turns[start:] {
		lines = append(lines, t.speaker+": "+t.text)
	}
	return strings.Join(lines, "\n\n"), nil
}

func transcriptText(raw json.RawMessage) string {
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
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

// Injected wrappers are not things anyone said.
func cleanTranscriptText(s string) string {
	for {
		open := strings.Index(s, "<system-reminder>")
		if open < 0 {
			break
		}
		close := strings.Index(s[open:], "</system-reminder>")
		if close < 0 {
			s = s[:open]
			break
		}
		s = s[:open] + s[open+close+len("</system-reminder>"):]
	}
	return strings.TrimSpace(s)
}

// --- the model call -------------------------------------------------------

const harnessSystemPrompt = "You are the assistant in an ongoing conversation " +
	"with an engineer. You will be shown the recent turns, your last message, and " +
	"the engineer's reply to it. Write your revised version of that last message " +
	"and nothing else: no preamble, no sign-off, no explanation of what you " +
	"changed. Use no tools."

func askForRewrite(history, dense, nudge string) (string, error) {
	var b strings.Builder
	if history != "" {
		fmt.Fprintf(&b, "Earlier in the conversation:\n\n%s\n\n", history)
	}
	fmt.Fprintf(&b, "Your last message was:\n\n%s\n\n", dense)
	fmt.Fprintf(&b, "The engineer replied:\n\n%s\n\n", nudge)
	b.WriteString("Write your revised message.")

	cmd := exec.Command("claude", "-p",
		"--strict-mcp-config",
		"--model", harnessModel,
		"--max-turns", "1",
		"--system-prompt", harnessSystemPrompt)
	cmd.Stdin = strings.NewReader(b.String())
	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("claude: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return "", fmt.Errorf("claude returned nothing: %s", strings.TrimSpace(stderr.String()))
	}
	return text, nil
}
