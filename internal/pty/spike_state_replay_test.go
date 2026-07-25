package pty

// SPIKE — throwaway. Replays recorded PTY captures through the current
// screen-scraping detector and through a candidate OSC-based detector, and
// scores both against hook-derived ground truth.
//
// Run with:
//   ATTN_SPIKE_CAPTURES=/path/to/spike go test ./internal/pty -run TestSpike -v
//
// A capture dir holds:
//   stream.jsonl  {"t": <sec since start>, "b64": "<raw pty bytes>"}
//   hooks.log     "<unix epoch>\t<HookName>\t<payload prefix>"

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---------- capture loading ----------

type spikeChunk struct {
	T   float64 `json:"t"`
	B64 string  `json:"b64"`
}

type spikeHook struct {
	At   float64 // seconds since capture start
	Name string
}

type spikeCapture struct {
	Name   string
	Chunks []spikeChunk
	Hooks  []spikeHook
	End    float64
	// AutoMode marks a capture launched with an automated permission reviewer
	// (claude defaultMode:auto / codex auto_review), where a PermissionRequest
	// never reaches the user.
	AutoMode bool
}

func loadSpikeCapture(t *testing.T, dir string) spikeCapture {
	t.Helper()
	cap := spikeCapture{Name: filepath.Base(dir)}

	f, err := os.Open(filepath.Join(dir, "stream.jsonl"))
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<22)
	for sc.Scan() {
		var c spikeChunk
		if json.Unmarshal(sc.Bytes(), &c) == nil {
			cap.Chunks = append(cap.Chunks, c)
		}
	}
	if len(cap.Chunks) == 0 {
		t.Fatalf("%s: empty stream", dir)
	}
	cap.End = cap.Chunks[len(cap.Chunks)-1].T

	// hooks.log carries absolute epochs; rebase onto the stream clock using the
	// SessionStart hook, which fires while the first chunks are being written.
	raw, err := os.ReadFile(filepath.Join(dir, "hooks.log"))
	if err != nil {
		return cap
	}
	var epochs []spikeHook
	for _, line := range strings.Split(string(raw), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		ts, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			continue
		}
		epochs = append(epochs, spikeHook{At: ts, Name: parts[1]})
	}
	if len(epochs) == 0 {
		return cap
	}

	// Rebase hooks onto the stream clock. SessionStart is a poor anchor (it fires
	// seconds after the process starts, and that boot delay varies), so anchor on
	// the first UserPromptSubmit, which fires the instant the capture script sent
	// its scripted Enter at a time we know exactly.
	base := epochs[0].At
	var anchor struct {
		FirstPromptAt float64 `json:"first_prompt_at"`
		AutoMode      bool    `json:"auto_mode"`
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "anchor.json")); err == nil {
		if json.Unmarshal(raw, &anchor) == nil && anchor.FirstPromptAt > 0 {
			cap.AutoMode = anchor.AutoMode
			for _, h := range epochs {
				if h.Name == "UserPromptSubmit" {
					base = h.At - anchor.FirstPromptAt
					break
				}
			}
		}
	}
	for _, h := range epochs {
		cap.Hooks = append(cap.Hooks, spikeHook{At: h.At - base, Name: h.Name})
	}
	return cap
}

// spikeLatency measures how long after a hook-truth transition the PTY-only
// signal followed. Negative means the PTY signal led the hook.
func spikeLatency(titles []spikeTitle, hookAt float64, wantBusy bool) (float64, bool) {
	for _, ti := range titles {
		if !ti.Known || ti.At < hookAt-5 {
			continue
		}
		if ti.Busy == wantBusy && ti.At >= hookAt-5 {
			return ti.At - hookAt, true
		}
	}
	return 0, false
}

// ---------- ground truth ----------

const (
	gtWorking  = "working"
	gtApproval = "pending_approval"
	gtSettled  = "settled" // idle or waiting_input; the classifier's job to split
)

// groundTruth turns the hook timeline into an interval function. Hooks are
// harness-authoritative: the agent itself reports when a turn starts, when it
// blocks on permission, and when it yields.
func groundTruth(c spikeCapture) func(float64) string {
	type mark struct {
		at    float64
		state string
	}
	var marks []mark
	for _, h := range c.Hooks {
		switch h.Name {
		case "UserPromptSubmit", "PostToolUse", "PreToolUse":
			marks = append(marks, mark{h.At, gtWorking})
		case "PermissionRequest":
			// A PermissionRequest only means "the USER is being asked" when no
			// automated reviewer is in the loop. Under claude defaultMode:auto or
			// codex auto_review the guardian resolves it in milliseconds and no
			// human is ever involved, so that window is tool execution (working).
			// There is no hook at the moment the guardian approves, so this cannot
			// be inferred from the timeline — it is a property of the launch, which
			// is exactly how the daemon will know it (ReviewerInLoop).
			if c.AutoMode {
				marks = append(marks, mark{h.At, gtWorking})
			} else {
				marks = append(marks, mark{h.At, gtApproval})
			}
		case "Stop":
			marks = append(marks, mark{h.At, gtSettled})
		}
	}
	sort.SliceStable(marks, func(i, j int) bool { return marks[i].at < marks[j].at })
	return func(t float64) string {
		state := gtSettled
		for _, m := range marks {
			if m.at > t {
				break
			}
			state = m.state
		}
		return state
	}
}

// ---------- candidate detector: OSC title heartbeat ----------

// spikeOSCDetector is the candidate. It reads ONLY what the harness publishes
// on the wire: the OSC 0 title (whose leading glyph is a spinner while a turn
// runs) and OSC 777 notifications. No screen scraping, no keyword lists.
type spikeOSCDetector struct {
	pending    []byte
	lastBusyAt float64
	lastIdleAt float64
	sawNotify  bool
	notifyAt   float64
	titles     []spikeTitle
	busyTTL    float64
}

type spikeTitle struct {
	At    float64
	Text  string
	Busy  bool
	Known bool
}

func newSpikeOSCDetector(busyTTL float64) *spikeOSCDetector {
	return &spikeOSCDetector{busyTTL: busyTTL, lastBusyAt: -1e9, lastIdleAt: -1e9, notifyAt: -1e9}
}

// titleBusy classifies a title's leading glyph.
//   - U+2800..U+28FF (braille) => spinner frame => a turn is RUNNING.
//     Both claude and codex animate their title with braille frames.
//   - U+2722..U+274B (dingbat stars, claude's ✳) => turn NOT running.
//   - anything else (codex's bare cwd, plain text) => turn NOT running.
func titleBusy(title string) (busy bool, known bool) {
	trimmed := strings.TrimLeft(title, " \t")
	if trimmed == "" {
		return false, false
	}
	r := []rune(trimmed)[0]
	switch {
	case r >= 0x2800 && r <= 0x28FF:
		return true, true
	case r >= 0x2722 && r <= 0x274B:
		return false, true
	default:
		return false, true
	}
}

func (d *spikeOSCDetector) feed(at float64, data []byte) {
	d.pending = append(d.pending, data...)
	for {
		start := indexOSC(d.pending)
		if start < 0 {
			// keep a small tail in case an OSC straddles chunks
			if len(d.pending) > 4096 {
				d.pending = d.pending[len(d.pending)-64:]
			}
			return
		}
		body, next, ok := scanOSCBody(d.pending, start)
		if !ok {
			d.pending = d.pending[start:]
			return
		}
		d.consumeOSC(at, body)
		d.pending = d.pending[next:]
	}
}

func indexOSC(buf []byte) int {
	for i := 0; i+1 < len(buf); i++ {
		if buf[i] == 0x1b && buf[i+1] == ']' {
			return i
		}
	}
	return -1
}

// scanOSCBody returns the payload between ESC] and its BEL/ST terminator.
func scanOSCBody(buf []byte, start int) (body string, next int, ok bool) {
	i := start + 2
	for i < len(buf) {
		if buf[i] == 0x07 {
			return string(buf[start+2 : i]), i + 1, true
		}
		if buf[i] == 0x1b && i+1 < len(buf) && buf[i+1] == '\\' {
			return string(buf[start+2 : i]), i + 2, true
		}
		i++
	}
	return "", 0, false
}

func (d *spikeOSCDetector) consumeOSC(at float64, body string) {
	switch {
	case strings.HasPrefix(body, "0;"), strings.HasPrefix(body, "2;"):
		title := body[2:]
		busy, known := titleBusy(title)
		d.titles = append(d.titles, spikeTitle{At: at, Text: title, Busy: busy, Known: known})
		if !known {
			return
		}
		if busy {
			d.lastBusyAt = at
		} else {
			d.lastIdleAt = at
		}
	case strings.HasPrefix(body, "777;notify"):
		d.sawNotify = true
		d.notifyAt = at
	}
}

// state resolves at an arbitrary time — the point of a level signal is that it
// answers on a tick, not only when bytes arrive.
func (d *spikeOSCDetector) state(now float64) string {
	if now-d.lastBusyAt <= d.busyTTL {
		return gtWorking
	}
	if d.lastIdleAt > d.lastBusyAt || d.sawNotify {
		return gtSettled
	}
	if d.lastBusyAt < -1e8 {
		return "unknown"
	}
	return gtSettled
}

// ---------- scoring ----------

type spikeScore struct {
	Samples   int
	Correct   int
	Confusion map[string]int // "truth->guess"
}

func newScore() *spikeScore { return &spikeScore{Confusion: map[string]int{}} }

func (s *spikeScore) add(truth, guess string) {
	s.Samples++
	if truth == guess {
		s.Correct++
		return
	}
	s.Confusion[truth+"->"+guess]++
}

func (s *spikeScore) pct() float64 {
	if s.Samples == 0 {
		return 0
	}
	return 100 * float64(s.Correct) / float64(s.Samples)
}

func spikeDirs(t *testing.T) []string {
	root := strings.TrimSpace(os.Getenv("ATTN_SPIKE_CAPTURES"))
	if root == "" {
		t.Skip("set ATTN_SPIKE_CAPTURES to a dir holding run_* captures")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read captures: %v", err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "run_") {
			dirs = append(dirs, filepath.Join(root, e.Name()))
		}
	}
	sort.Strings(dirs)
	return dirs
}

// TestSpikeReplay scores the current detector against the OSC candidate.
func TestSpikeReplay(t *testing.T) {
	const sampleStep = 0.2
	const busyTTL = 3.0

	for _, dir := range spikeDirs(t) {
		cap := loadSpikeCapture(t, dir)
		if len(cap.Hooks) == 0 {
			// No hook ground truth (e.g. codex, which attn wires differently).
			// Still report the heartbeat's shape, which is what the design leans on.
			osc := newSpikeOSCDetector(busyTTL)
			for _, c := range cap.Chunks {
				data, _ := base64.StdEncoding.DecodeString(c.B64)
				osc.feed(c.T, data)
			}
			fmt.Printf("\n=== %s (%.0fs, %d chunks, NO hook truth)\n", cap.Name, cap.End, len(cap.Chunks))
			fmt.Printf("  title emissions: %d (busy=%d idle=%d)  median busy interval %.3fs  max gap %.2fs\n",
				len(osc.titles), countBusy(osc.titles, true), countBusy(osc.titles, false),
				medianBusyInterval(osc.titles), maxBusyGap(osc.titles))
			continue
		}
		truth := groundTruth(cap)

		current := newClaudeWorkingDetector()
		osc := newSpikeOSCDetector(busyTTL)

		// currentState tracks what the existing detector last emitted; it is an
		// edge emitter, so its state persists until it emits again.
		currentState := "unknown"
		curScore, oscScore := newScore(), newScore()

		chunkIdx := 0
		var currentTimeline, oscTimeline []string
		for now := 0.0; now <= cap.End; now += sampleStep {
			for chunkIdx < len(cap.Chunks) && cap.Chunks[chunkIdx].T <= now {
				data, _ := base64.StdEncoding.DecodeString(cap.Chunks[chunkIdx].B64)
				if s, changed := current.Observe(data); changed {
					currentState = normalizeSpikeState(s)
				}
				osc.feed(cap.Chunks[chunkIdx].T, data)
				chunkIdx++
			}
			tr := truth(now)
			// Ignore the boot window before the first hook fired.
			if now < firstHookAt(cap) {
				continue
			}
			curScore.add(tr, currentState)
			oscScore.add(tr, osc.state(now))
			currentTimeline = append(currentTimeline, boolStr(currentState == tr))
			oscTimeline = append(oscTimeline, boolStr(osc.state(now) == tr))
		}

		fmt.Printf("\n=== %s (%.0fs, %d chunks, %d hooks)\n", cap.Name, cap.End, len(cap.Chunks), len(cap.Hooks))
		fmt.Printf("  current (screen scrape): %5.1f%% correct  worst-wrong-streak %5.1fs  %v\n",
			curScore.pct(), worstStreak(currentTimeline, sampleStep), topConfusion(curScore))
		fmt.Printf("  candidate (OSC title)  : %5.1f%% correct  worst-wrong-streak %5.1fs  %v\n",
			oscScore.pct(), worstStreak(oscTimeline, sampleStep), topConfusion(oscScore))
		fmt.Printf("  title emissions: %d  (busy=%d idle=%d)  osc777=%v\n",
			len(osc.titles), countBusy(osc.titles, true), countBusy(osc.titles, false), osc.sawNotify)
		fmt.Printf("  max gap between busy titles while running: %.2fs\n", maxBusyGap(osc.titles))
		fmt.Printf("  longest FALSE-SETTLED window mid-turn: %.2fs\n", falseSettled(cap, truth, busyTTL, sampleStep))
		for _, h := range cap.Hooks {
			switch h.Name {
			case "UserPromptSubmit":
				if lat, ok := spikeLatency(osc.titles, h.At, true); ok {
					fmt.Printf("  turn start  @%6.1fs -> spinner appeared %+.2fs later\n", h.At, lat)
				}
			case "Stop":
				if lat, ok := spikeLatency(osc.titles, h.At, false); ok {
					fmt.Printf("  turn settle @%6.1fs -> idle glyph   %+.2fs later\n", h.At, lat)
				}
			}
		}
	}
}

func boolStr(ok bool) string {
	if ok {
		return "."
	}
	return "X"
}

// worstStreak is the longest continuous stretch of disagreement with truth —
// the direct measure of "the color got stuck".
func worstStreak(marks []string, step float64) float64 {
	worst, run := 0, 0
	for _, m := range marks {
		if m == "X" {
			run++
			if run > worst {
				worst = run
			}
		} else {
			run = 0
		}
	}
	return float64(worst) * step
}

func normalizeSpikeState(s string) string {
	switch s {
	case stateWorking:
		return gtWorking
	case statePendingApproval:
		return gtApproval
	case stateIdle, stateWaitingInput:
		return gtSettled
	default:
		return s
	}
}

func firstHookAt(c spikeCapture) float64 {
	if len(c.Hooks) == 0 {
		return 0
	}
	return c.Hooks[0].At
}

func countBusy(titles []spikeTitle, busy bool) int {
	n := 0
	for _, ti := range titles {
		if ti.Known && ti.Busy == busy {
			n++
		}
	}
	return n
}

// maxBusyGap is the longest interval between consecutive busy-title emissions,
// bounded by the following idle title. It sizes the heartbeat TTL: the TTL must
// exceed this or a running turn will flicker to settled.
func maxBusyGap(titles []spikeTitle) float64 {
	worst := 0.0
	prev := -1.0
	for _, ti := range titles {
		if !ti.Known {
			continue
		}
		if !ti.Busy {
			prev = -1
			continue
		}
		if prev >= 0 && ti.At-prev > worst {
			worst = ti.At - prev
		}
		prev = ti.At
	}
	return worst
}

// falseSettled is the metric that matters for an attention mode: the longest
// stretch where truth says the agent is working but the heartbeat-only detector
// says settled. A false settle is what would make a settled/unsettled sidebar
// declare an agent done while it is mid-tool.
func falseSettled(cap spikeCapture, truth func(float64) string, ttl, step float64) float64 {
	osc := newSpikeOSCDetector(ttl)
	idx := 0
	worst, run := 0.0, 0.0
	for now := 0.0; now <= cap.End; now += step {
		for idx < len(cap.Chunks) && cap.Chunks[idx].T <= now {
			data, _ := base64.StdEncoding.DecodeString(cap.Chunks[idx].B64)
			osc.feed(cap.Chunks[idx].T, data)
			idx++
		}
		if truth(now) == gtWorking && osc.state(now) != gtWorking {
			run += step
			if run > worst {
				worst = run
			}
		} else {
			run = 0
		}
	}
	return worst
}

func medianBusyInterval(titles []spikeTitle) float64 {
	var gaps []float64
	prev := -1.0
	for _, ti := range titles {
		if !ti.Known || !ti.Busy {
			prev = -1
			continue
		}
		if prev >= 0 {
			gaps = append(gaps, ti.At-prev)
		}
		prev = ti.At
	}
	if len(gaps) == 0 {
		return 0
	}
	sort.Float64s(gaps)
	return gaps[len(gaps)/2]
}

func topConfusion(s *spikeScore) string {
	type kv struct {
		k string
		v int
	}
	var all []kv
	for k, v := range s.Confusion {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v })
	var parts []string
	for i, e := range all {
		if i == 3 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s x%d", e.k, e.v))
	}
	if len(parts) == 0 {
		return "(clean)"
	}
	return strings.Join(parts, ", ")
}

// TestSpikeTTLSweep sizes the heartbeat TTL. TTL=0 means "trust the idle glyph
// as an immediate edge"; larger values absorb mid-turn idle blips at the cost of
// settling late.
func TestSpikeTTLSweep(t *testing.T) {
	const sampleStep = 0.2
	ttls := []float64{0, 0.5, 1, 1.5, 2, 3, 4, 5, 6}

	fmt.Printf("\n%-8s %10s %10s %14s %14s\n", "TTL", "correct%", "worstWrong", "late-settle(s)", "falseSettled(s)")
	for _, ttl := range ttls {
		var samples, correct int
		worst := 0.0
		worstFalseSettled := 0.0
		lateTotal, lateN := 0.0, 0
		for _, dir := range spikeDirs(t) {
			cap := loadSpikeCapture(t, dir)
			if len(cap.Hooks) == 0 {
				continue
			}
			truth := groundTruth(cap)
			osc := newSpikeOSCDetector(ttl)
			idx := 0
			var marks []string
			for now := 0.0; now <= cap.End; now += sampleStep {
				for idx < len(cap.Chunks) && cap.Chunks[idx].T <= now {
					data, _ := base64.StdEncoding.DecodeString(cap.Chunks[idx].B64)
					osc.feed(cap.Chunks[idx].T, data)
					idx++
				}
				if now < firstHookAt(cap) {
					continue
				}
				tr := truth(now)
				guess := osc.state(now)
				// Approvals are hook-owned by design; the PTY signal cannot see
				// them, so exclude those windows from the busy/settled scoring.
				if tr == gtApproval {
					continue
				}
				samples++
				if tr == guess {
					correct++
					marks = append(marks, ".")
				} else {
					marks = append(marks, "X")
				}
			}
			if w := worstStreak(marks, sampleStep); w > worst {
				worst = w
			}
			if fs := falseSettled(cap, truth, ttl, sampleStep); fs > worstFalseSettled {
				worstFalseSettled = fs
			}
			// How long after each Stop did the detector still claim working?
			for _, h := range cap.Hooks {
				if h.Name != "Stop" {
					continue
				}
				for dt := 0.0; dt < 20; dt += sampleStep {
					if osc.state(h.At+dt) != gtWorking {
						lateTotal += dt
						lateN++
						break
					}
				}
			}
		}
		late := 0.0
		if lateN > 0 {
			late = lateTotal / float64(lateN)
		}
		fmt.Printf("%-8.1f %9.1f%% %9.1fs %13.2f %14.2f\n",
			ttl, 100*float64(correct)/float64(samples), worst, late, worstFalseSettled)
	}
}

// TestSpikeTitleHijack is the adversarial case: a subprocess the agent runs
// (a shell, vim, ssh, docker) sets the terminal title itself, overwriting the
// harness spinner. If the heartbeat premise is fragile, this breaks it.
func TestSpikeTitleHijack(t *testing.T) {
	const sampleStep = 0.2
	const busyTTL = 3.0

	for _, mode := range []string{"none", "hijack_every_2s", "hijack_burst"} {
		var samples, correct int
		worst := 0.0
		for _, dir := range spikeDirs(t) {
			cap := loadSpikeCapture(t, dir)
			if len(cap.Hooks) == 0 {
				continue
			}
			truth := groundTruth(cap)
			osc := newSpikeOSCDetector(busyTTL)
			idx := 0
			var marks []string
			nextHijack := 0.0
			for now := 0.0; now <= cap.End; now += sampleStep {
				for idx < len(cap.Chunks) && cap.Chunks[idx].T <= now {
					data, _ := base64.StdEncoding.DecodeString(cap.Chunks[idx].B64)
					osc.feed(cap.Chunks[idx].T, data)
					idx++
				}
				switch mode {
				case "hijack_every_2s":
					if now >= nextHijack {
						osc.feed(now, []byte("\x1b]0;victor@mac: ~/projects/attn\x07"))
						nextHijack = now + 2
					}
				case "hijack_burst":
					// A TUI repainting its title continuously (e.g. htop, ssh).
					if now > 10 && now < 40 {
						osc.feed(now, []byte("\x1b]0;htop\x07"))
					}
				}
				if now < firstHookAt(cap) {
					continue
				}
				tr := truth(now)
				if tr == gtApproval {
					continue
				}
				samples++
				if tr == osc.state(now) {
					correct++
					marks = append(marks, ".")
				} else {
					marks = append(marks, "X")
				}
			}
			if w := worstStreak(marks, sampleStep); w > worst {
				worst = w
			}
		}
		fmt.Printf("  hijack mode %-16s -> %5.1f%% correct, worst wrong streak %.1fs\n",
			mode, 100*float64(correct)/float64(samples), worst)
	}
}

var _ = time.Second
