package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/prompts"
)

func operate(t *testing.T, e *editor, q operationRequest) any {
	t.Helper()
	result, err := e.operation(t.Context(), q)
	if err != nil {
		t.Fatalf("%s: %v", q.Op, err)
	}
	return result
}
func expectConflict(t *testing.T, err error) {
	t.Helper()
	var detail *operationError
	if !errors.As(err, &detail) || detail.Code != "revision_conflict" {
		t.Fatalf("expected revision conflict, got %v", err)
	}
}
func newDraft(t *testing.T, e *editor) sharedDraft {
	t.Helper()
	return operate(t, e, operationRequest{Op: "draft-create", Title: "Clarify wake", Focus: &focus{Event: "crew/wake", Values: prompts.Values{}}}).(sharedDraft)
}
func sourceTest(t *testing.T, e *editor, name string) source {
	t.Helper()
	data, err := e.root.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return source{string(data), revision(data)}
}
func putTest(t *testing.T, e *editor, d sharedDraft, name, text string) sharedDraft {
	t.Helper()
	expected := sourceTest(t, e, name).Revision
	if f, ok := d.Files[name]; ok {
		expected = f.Revision
	}
	return operate(t, e, operationRequest{Op: "draft-put", ID: d.ID, Path: name, Text: text, Expect: expected, Author: "agent"}).(sharedDraft)
}
func runTestCLI(t *testing.T, e *editor, input string, args ...string) (int, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	code := runCLI(t.Context(), append([]string{"--repo", e.repo, "--json"}, args...), strings.NewReader(input), &out, &stderr)
	if stderr.Len() > 0 {
		t.Log(stderr.String())
	}
	return code, out.String()
}

func TestHeadlessDiscoveryUsesCheckoutDeclarations(t *testing.T) {
	e := testEditor(t)
	manifestTest(t, e, prompts.Recipient{ID: "custom", Events: []prompts.Event{prompts.On("launch", "user_message", "New checkout event", prompts.Input(prompts.TextField("brief", "Required input")))}})
	code, out := runTestCLI(t, e, "", "inspect", "custom/launch")
	if code != 0 || !strings.Contains(out, `"name": "brief"`) {
		t.Fatalf("inspect needs no values: %d %s", code, out)
	}
	code, out = runTestCLI(t, e, "", "inspect", "custom/launch", "--set", "brief=A checkout input")
	if code != 0 || !strings.Contains(out, "A checkout input") {
		t.Fatalf("render: %d %s", code, out)
	}
	code, out = runTestCLI(t, e, "", "list")
	if code != 0 || strings.Contains(out, "session/launch") {
		t.Fatalf("embedded definitions leaked: %d %s", code, out)
	}
	if err := e.root.Remove(prompts.ManifestPath); err != nil {
		t.Fatal(err)
	}
	code, out = runTestCLI(t, e, "", "list")
	if code != 2 || !strings.Contains(out, "refresh") {
		t.Fatalf("missing manifest must not fall back: %d %s", code, out)
	}
}

func TestConcurrentDraftWritersAndIndependentFiles(t *testing.T) {
	e := testEditor(t)
	d := newDraft(t, e)
	name := "content/crew/wake.md"
	original := sourceTest(t, e, name)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, text := range []string{"Maintainer edit.", "Agent edit."} {
		go func(text string) {
			other, err := openEditor(e.repo)
			if err != nil {
				results <- err
				return
			}
			defer other.root.Close()
			<-start
			_, err = other.operation(context.Background(), operationRequest{Op: "draft-put", ID: d.ID, Path: name, Text: text, Expect: original.Revision})
			results <- err
		}(text)
	}
	close(start)
	wins := 0
	for range 2 {
		if err := <-results; err == nil {
			wins++
		} else {
			expectConflict(t, err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d writers won", wins)
	}
	d = operate(t, e, operationRequest{Op: "draft-get", ID: d.ID}).(sharedDraft)
	if sourceTest(t, e, name) != original {
		t.Fatal("draft wrote checkout")
	}
	d = putTest(t, e, d, "content/crew/heartbeat.md", "Independent edit.")
	if len(d.Files) != 2 {
		t.Fatal("one writer lost another file")
	}
	code, out := runTestCLI(t, e, "Stale", "draft", "put", d.ID, name, "--file", "-", "--expect", original.Revision)
	if code != 3 || !strings.Contains(out, `"current"`) {
		t.Fatalf("conflict receipt: %d %s", code, out)
	}
}

func TestReviewSurvivesEditsAndRestartWithAnchoredFeedback(t *testing.T) {
	e := testEditor(t)
	d := newDraft(t, e)
	name := "content/crew/wake.md"
	d = putTest(t, e, d, name, "Review this exact sentence.\n")
	reviewed := operate(t, e, operationRequest{Op: "review-create", DraftID: d.ID, Revision: d.Revision}).(review)
	d = operate(t, e, operationRequest{Op: "draft-get", ID: d.ID}).(sharedDraft)
	d = putTest(t, e, d, name, "Later shared edit.\n")
	writeTest(t, e, name, "Later disk edit.\n")
	restarted, err := openEditor(e.repo)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.root.Close()
	inspected := operate(t, restarted, operationRequest{Op: "inspect", ReviewID: reviewed.ID}).(map[string]any)
	if inspected["result"].(prompts.Result).Text != "Review this exact sentence." {
		t.Fatal("review drifted")
	}
	r := operate(t, restarted, operationRequest{Op: "review-comment", ID: reviewed.ID, Text: "Make it shorter.", Target: "source", Path: name, Selection: "exact sentence", Author: "maintainer"}).(review)
	if len(r.Feedback) != 1 || r.Feedback[0].SourceRevision != reviewed.Snapshot.Sources[name].Revision {
		t.Fatal("comment lost its source revision")
	}
	_, err = restarted.operation(t.Context(), operationRequest{Op: "review-comment", ID: r.ID, Text: "Stale selection", Target: "prompt", Selection: "Later shared edit"})
	expectConflict(t, err)
	watched, err := restarted.watch(t.Context(), operationRequest{DraftID: d.ID}, d.Revision, time.Second)
	if err != nil || watched.(sharedDraft).LatestReview != r.ID {
		t.Fatalf("feedback not visible on draft: %v %v", watched, err)
	}
	code, out := runTestCLI(t, e, "", "inspect", "--review", r.ID)
	if code != 0 || !strings.Contains(out, "Review this exact sentence") || strings.Contains(out, "Later shared") {
		t.Fatalf("CLI review: %d %s", code, out)
	}
}

func TestDraftApplyValidatesWholeChangeAndRejectsDiskConflicts(t *testing.T) {
	e := testEditor(t)
	d := newDraft(t, e)
	a, b := "content/crew/wake.md", "content/crew/heartbeat.md"
	before := sourceTest(t, e, a)
	d = putTest(t, e, d, a, "Valid new wake.")
	d = putTest(t, e, d, b, "{{undeclared}}")
	_, err := e.operation(t.Context(), operationRequest{Op: "draft-apply", ID: d.ID, Revision: d.Revision})
	if err == nil || sourceTest(t, e, a) != before {
		t.Fatal("invalid transaction wrote sources")
	}
	d = putTest(t, e, d, b, "Valid new heartbeat.")
	writeTest(t, e, b, "External edit")
	_, err = e.operation(t.Context(), operationRequest{Op: "draft-apply", ID: d.ID, Revision: d.Revision})
	expectConflict(t, err)
	if sourceTest(t, e, a) != before || sourceTest(t, e, b).Text != "External edit" {
		t.Fatal("conflict wrote sources")
	}
	d = operate(t, e, operationRequest{Op: "draft-reset", ID: d.ID, Path: b, Expect: d.Files[b].Revision}).(sharedDraft)
	d = putTest(t, e, d, b, "Resolved heartbeat.")
	d = operate(t, e, operationRequest{Op: "draft-apply", ID: d.ID, Revision: d.Revision}).(sharedDraft)
	if len(d.Files) != 0 || sourceTest(t, e, a).Text != "Valid new wake." || sourceTest(t, e, b).Text != "Resolved heartbeat." {
		t.Fatal("apply incomplete")
	}
	d = operate(t, e, operationRequest{Op: "draft-archive", ID: d.ID, Revision: d.Revision}).(sharedDraft)
	if _, err = e.operation(t.Context(), operationRequest{Op: "draft-put", ID: d.ID, Path: a, Text: "oops", Expect: sourceTest(t, e, a).Revision}); err == nil {
		t.Fatal("archived edit allowed")
	}
	d = operate(t, e, operationRequest{Op: "draft-restore", ID: d.ID, Revision: d.Revision}).(sharedDraft)
	if d.Archived {
		t.Fatal("restore failed")
	}
}

func TestInterruptedApplyRecovery(t *testing.T) {
	for _, state := range []string{"partial", "complete", "external-conflict"} {
		t.Run(state, func(t *testing.T) {
			e := testEditor(t)
			d := newDraft(t, e)
			a, b := "content/crew/wake.md", "content/crew/heartbeat.md"
			d = putTest(t, e, d, a, "After A")
			d = putTest(t, e, d, b, "After B")
			tx := applyTransaction{Files: map[string]fileWrite{a: {sourceTest(t, e, a).Text, "After A", 0644}, b: {sourceTest(t, e, b).Text, "After B", 0644}}, Draft: d}
			tx.Draft.Files = map[string]draftFile{}
			tx.Draft.Revision++
			root, err := e.stateRoot()
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if err = writeJSON(root, "apply.json", tx); err != nil {
				t.Fatal(err)
			}
			writeTest(t, e, a, "After A")
			if state == "complete" {
				writeTest(t, e, b, "After B")
			}
			if state == "external-conflict" {
				writeTest(t, e, b, "External")
			}
			result, err := e.operation(t.Context(), operationRequest{Op: "draft-get", ID: d.ID})
			if state == "external-conflict" {
				expectConflict(t, err)
				if sourceTest(t, e, b).Text != "External" {
					t.Fatal("external edit lost")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if state == "partial" && (sourceTest(t, e, a).Text != tx.Files[a].Before || len(result.(sharedDraft).Files) != 2) {
				t.Fatal("partial write not rolled back")
			}
			if state == "complete" && (sourceTest(t, e, a).Text != "After A" || len(result.(sharedDraft).Files) != 0) {
				t.Fatal("complete transaction not finalized")
			}
			if _, err = root.Stat("apply.json"); !os.IsNotExist(err) {
				t.Fatal("journal retained")
			}
		})
	}
}

func TestScenarioProducerDiffAndFrozenScenarios(t *testing.T) {
	e := testEditor(t)
	gitTest(t, e, "init", "-b", "next")
	name := "content/producer.md"
	writeTest(t, e, name, "Before producer.")
	manifestTest(t, e, prompts.Recipient{ID: "example", Events: []prompts.Event{
		prompts.On("producer", "user_message", "", prompts.Use("producer", name)),
		prompts.On("consumer", "user_message", "", prompts.Input(prompts.ProducedBy(prompts.TextField("body", "Producer output"), "example/producer"))),
	}})
	for id, body := range map[string]string{
		"producer": `{"version":1,"id":"producer","recipient":"example","event":"producer","values":{}}`,
		"consumer": `{"version":1,"id":"consumer","recipient":"example","event":"consumer","values":{},"inputs":{"body":"producer"}}`,
	} {
		writeTest(t, e, "scenarios/"+id+".json", body)
	}
	base := commitTest(t, e)
	writeTest(t, e, name, "After producer.")
	result := operate(t, e, operationRequest{Op: "compare", Base: base, Mode: "tip", Scenario: "consumer"}).(map[string]any)
	c := result["scenarios"].([]scenarioCheck)[0]
	if !c.Changed || c.Base.Text != "Before producer." || c.Current.Text != "After producer." {
		t.Fatalf("producer hidden: %+v", c)
	}
	if _, ok := result["affected_events"].(map[string]string)["example/consumer"]; !ok {
		t.Fatal("missing indirect usage")
	}
	w := request(t, e, "compare", editRequest{BaseCommit: base, Recipient: "example", Event: "consumer", Scenario: "consumer"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "-Before producer.") || !strings.Contains(w.Body.String(), "+After producer.") {
		t.Fatalf("browser producer diff: %s", w.Body)
	}
	d := operate(t, e, operationRequest{Op: "draft-create", Focus: &focus{Event: "example/consumer", Scenario: "consumer"}}).(sharedDraft)
	r := operate(t, e, operationRequest{Op: "review-create", DraftID: d.ID, Revision: d.Revision}).(review)
	writeTest(t, e, "scenarios/consumer.json", `{"version":1,"id":"consumer","recipient":"example","event":"consumer","values":{"body":"Changed scenario"}}`)
	inspected := operate(t, e, operationRequest{Op: "inspect", ReviewID: r.ID, Scenario: "consumer"}).(map[string]any)
	if inspected["result"].(prompts.Result).Text != "After producer." {
		t.Fatal("review scenario drifted")
	}
}

func TestScenarioSaveAndDefinitionsRevision(t *testing.T) {
	e := testEditor(t)
	s := operate(t, e, operationRequest{Op: "scenario-save", ID: "wake", Event: "crew/wake", Values: prompts.Values{}}).(scenario)
	_, err := e.operation(t.Context(), operationRequest{Op: "scenario-save", ID: s.ID, Event: "crew/wake", Values: prompts.Values{}})
	expectConflict(t, err)
	operate(t, e, operationRequest{Op: "scenario-save", ID: s.ID, Event: "crew/wake", Expect: s.Revision, Values: prompts.Values{}})
	snapshot, err := e.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	m, err := prompts.ParseManifest(snapshot.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	m.DefinitionsHash, err = prompts.DefinitionsHash(e.root.FS())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(m)
	writeTest(t, e, prompts.ManifestPath, string(data))
	writeTest(t, e, "new.go", "package prompts\n")
	code, out := runTestCLI(t, e, "", "inspect", "crew/wake")
	if code != 2 || !strings.Contains(out, "composition declarations changed in Go") {
		t.Fatalf("stale definitions unnoticed: %d %s", code, out)
	}
}

func TestWatchReceivesConcurrentFeedback(t *testing.T) {
	e := testEditor(t)
	d := newDraft(t, e)
	r := operate(t, e, operationRequest{Op: "review-create", DraftID: d.ID, Revision: d.Revision}).(review)
	var wg sync.WaitGroup
	wg.Add(1)
	done := make(chan error, 1)
	go func() {
		defer wg.Done()
		result, err := e.watch(t.Context(), operationRequest{ReviewID: r.ID}, 0, 5*time.Second)
		if err == nil {
			if got, ok := result.(review); !ok || len(got.Feedback) != 1 {
				err = fmt.Errorf("watch result: %v", result)
			}
		}
		done <- err
	}()
	operate(t, e, operationRequest{Op: "review-comment", ID: r.ID, Text: "Feedback after start"})
	wg.Wait()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestStateAndScenarioPathsStayInsideCheckout(t *testing.T) {
	e := testEditor(t)
	for _, q := range []operationRequest{{Op: "draft-get", ID: "../../outside"}, {Op: "review-get", ID: "../outside"}, {Op: "scenario-save", ID: "../outside", Event: "crew/wake"}} {
		if _, err := e.operation(t.Context(), q); err == nil {
			t.Fatalf("accepted path traversal: %+v", q)
		}
	}
	if _, err := os.Stat(filepath.Join(e.repo, "outside.json")); !os.IsNotExist(err) {
		t.Fatal("wrote outside state")
	}
}

func TestRefreshReloadsChangedGoDeclarationsWithoutRestart(t *testing.T) {
	e := testEditor(t)
	files, err := filepath.Glob("../../internal/prompts/*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		writeTest(t, e, filepath.Base(name), string(data))
	}
	protocolDir := filepath.Join(e.repo, "internal/protocol")
	if err := os.MkdirAll(protocolDir, 0755); err != nil {
		t.Fatal(err)
	}
	protocolTypes, err := os.ReadFile("../../internal/protocol/generated.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(protocolDir, "generated.go"), protocolTypes, 0644); err != nil {
		t.Fatal(err)
	}
	generator, err := os.ReadFile("../promptgen/main.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cmd/promptgen", "plugins/attn-pi/automode", "app/src/prompts"} {
		if err := os.MkdirAll(filepath.Join(e.repo, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(e.repo, "cmd/promptgen/main.go"), generator, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.repo, "go.mod"), []byte("module github.com/victorarias/attn\n\ngo 1.25.3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	operate(t, e, operationRequest{Op: "refresh"})
	var declaration string
	for _, name := range files {
		data, err := e.root.ReadFile(filepath.Base(name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `On("wake",`) {
			declaration = filepath.Base(name)
			writeTest(t, e, declaration, strings.Replace(string(data), `On("wake",`, `On("wake-renamed",`, 1))
			break
		}
	}
	if declaration == "" {
		t.Fatal("wake declaration missing")
	}
	if _, err := e.operation(t.Context(), operationRequest{Op: "inspect", Event: "crew/wake"}); err == nil {
		t.Fatal("stale catalog accepted")
	}
	operate(t, e, operationRequest{Op: "refresh"})
	result := operate(t, e, operationRequest{Op: "inspect", Event: "crew/wake-renamed", Values: prompts.Values{}}).(map[string]any)
	if result["result"].(prompts.Result).Text == "" {
		t.Fatal("refreshed event not rendered")
	}
	if _, err := e.operation(t.Context(), operationRequest{Op: "inspect", Event: "crew/wake"}); err == nil {
		t.Fatal("old event survived refresh")
	}
	writeTest(t, e, declaration, "invalid Go syntax")
	if _, err := e.operation(t.Context(), operationRequest{Op: "refresh"}); err == nil {
		t.Fatal("bad Go accepted")
	}
}

func TestDraftSyncAfterCompositionChangesAndSourceRemoval(t *testing.T) {
	e := testEditor(t)
	d := newDraft(t, e)
	name := "content/crew/wake.md"
	d = putTest(t, e, d, name, "Keep this edited source.")
	snapshot, err := e.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := prompts.ParseManifest(snapshot.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	for i := range manifest.Recipients {
		if manifest.Recipients[i].ID == "crew" {
			for j := range manifest.Recipients[i].Events {
				if manifest.Recipients[i].Events[j].ID == "wake" {
					manifest.Recipients[i].Events[j].ID = "wake-new"
				}
			}
		}
	}
	manifestTest(t, e, manifest.Recipients...)
	_, err = e.operation(t.Context(), operationRequest{Op: "inspect", DraftID: d.ID, Event: "crew/wake-new"})
	expectConflict(t, err)
	d = operate(t, e, operationRequest{Op: "draft-sync", ID: d.ID, Revision: d.Revision}).(sharedDraft)
	inspected := operate(t, e, operationRequest{Op: "inspect", DraftID: d.ID, Event: "crew/wake-new", Values: prompts.Values{}}).(map[string]any)
	if inspected["result"].(prompts.Result).Text != "Keep this edited source." {
		t.Fatal("sync lost draft text")
	}
	for i := range manifest.Recipients {
		if manifest.Recipients[i].ID == "crew" {
			events := []prompts.Event{}
			for _, event := range manifest.Recipients[i].Events {
				if event.ID != "wake-new" {
					events = append(events, event)
				}
			}
			manifest.Recipients[i].Events = events
		}
	}
	manifestTest(t, e, manifest.Recipients...)
	_, err = e.operation(t.Context(), operationRequest{Op: "draft-sync", ID: d.ID, Revision: d.Revision})
	expectConflict(t, err)
	d = operate(t, e, operationRequest{Op: "draft-reset", ID: d.ID, Path: name, Expect: d.Files[name].Revision}).(sharedDraft)
	operate(t, e, operationRequest{Op: "draft-sync", ID: d.ID, Revision: d.Revision})
}

func TestScenarioBindingsRejectCyclesAndDuplicateValues(t *testing.T) {
	e := testEditor(t)
	manifestTest(t, e, prompts.Recipient{ID: "loop", Events: []prompts.Event{prompts.On("render", "user_message", "", prompts.Input(prompts.ProducedBy(prompts.TextField("body", "Bound input"), "loop/render")))}})
	writeTest(t, e, "scenarios/loop.json", `{"version":1,"id":"loop","recipient":"loop","event":"render","values":{},"inputs":{"body":"loop"}}`)
	result := operate(t, e, operationRequest{Op: "check"}).(map[string]any)
	if result["valid"] != false || !strings.Contains(result["scenarios"].([]scenarioCheck)[0].Error, "cycle") {
		t.Fatal("cycle accepted")
	}
	_, err := e.operation(t.Context(), operationRequest{Op: "scenario-save", ID: "duplicate", Event: "loop/render", Values: prompts.Values{"body": "literal"}, Inputs: map[string]string{"body": "loop"}})
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("duplicate binding accepted: %v", err)
	}
}
