package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

// Dreaming — harvest + read-only inspection (phase one).
//
// This file implements the deterministic, LLM-free harvest and the inspection
// commands that surface it (`attn notebook dream status` / `--dry-run`). It scans
// the three v1 sources — dated journals, canonical workspace-context snapshots,
// and closed chief-of-staff dispatches — into deduplicated candidates ordered by
// durability signal (recurrence across distinct contexts).
//
// Harvest here is preview-only: it never writes candidates.json, never advances a
// cursor, and never touches durable memory. Persistence, the nightly scheduler,
// and the gated LLM promote pass arrive in the follow-up dreaming PR; keeping
// this phase side-effect-free makes the harvest model observable and reviewable
// before anything runs autonomously.

// topDreamCandidates bounds how many candidates `dream status` returns inline.
const topDreamCandidates = 10

// maxDreamRunCandidates bounds how many candidates a `dream --dry-run` preview
// returns, so a large notebook can't produce an unbounded response.
const maxDreamRunCandidates = 200

// harvestDreamCandidates scans all three v1 sources into a merged candidate set.
// It is read-only: callers decide what to do with the result (status summary,
// dry-run preview). Failure to read any single source is logged and skipped — a
// partial harvest is more useful than none, and the preview is advisory.
func (d *Daemon) harvestDreamCandidates() (*notebook.DreamCandidateSet, error) {
	store, err := d.notebookStoreFor()
	if err != nil {
		return nil, err
	}
	set := notebook.NewDreamCandidateSet()

	// 1. Journals: each dated note's blocks (the raw system of record).
	entries, err := store.List(notebook.DirJournal)
	if err != nil {
		d.logf("dreaming harvest: list journals: %v", err)
	}
	for _, e := range entries {
		// attn never writes a note larger than MaxFileSize, so anything bigger is
		// an oversized externally-synced file. Skip it rather than pull its whole
		// body into memory (mirrors Store.Backlinks and List's scan cap).
		if e.Size > notebook.MaxFileSize {
			d.logf("dreaming harvest: skip oversized journal %s (%d bytes)", e.Path, e.Size)
			continue
		}
		content, _, rerr := store.Read(e.Path)
		if rerr != nil {
			d.logf("dreaming harvest: read %s: %v", e.Path, rerr)
			continue
		}
		date := notebook.JournalDateFromPath(e.Path)
		doc := notebook.ParsePermissive(content)
		for _, sig := range notebook.ExtractJournalSignals(e.Path, date, doc.Body) {
			set.Add(sig)
		}
	}

	// 2. Workspace-context snapshots: durable Decisions/Constraints per workspace.
	if d.store != nil {
		contexts, cerr := d.store.ListWorkspaceContexts()
		if cerr != nil {
			d.logf("dreaming harvest: list workspace contexts: %v", cerr)
		}
		for _, wc := range contexts {
			for _, sig := range extractContextSignals(wc.WorkspaceID, wc.Content, wc.UpdatedAt) {
				set.Add(sig)
			}
		}

		// 3. Closed dispatches: a dispatch is closed when its target session is gone.
		for _, disp := range d.store.ListChiefOfStaffDispatches("") {
			if disp == nil || d.store.Get(disp.SessionID) != nil {
				continue
			}
			for _, sig := range dispatchSignals(disp) {
				set.Add(sig)
			}
		}
	}

	return set, nil
}

// contextDurableHeadings are the workspace-context sections whose bullets are
// durable enough to harvest. Area/Current Picture/Threads are working state;
// Decisions and Constraints are the facts meant to outlive a workspace.
var contextDurableHeadings = map[string]bool{
	"## decisions":   true,
	"## constraints": true,
}

// extractContextSignals harvests each Decisions/Constraints bullet from a
// canonical workspace-context snapshot. The workspace is both the source ref and
// the distinct-context label, so a decision echoed across several workspaces
// reads as recurring.
func extractContextSignals(workspaceID, content, updatedAt string) []notebook.DreamSignal {
	ref := "context:" + workspaceID
	ctx := "workspace:" + workspaceID
	var out []notebook.DreamSignal
	for _, bullet := range collectBulletsUnderHeadings(content, contextDurableHeadings) {
		out = append(out, notebook.DreamSignal{
			Source:    notebook.SignalSourceContext,
			Text:      bullet,
			SourceRef: ref,
			Context:   ctx,
			Seen:      updatedAt,
		})
	}
	return out
}

// collectBulletsUnderHeadings returns the list bullets (with their continuation
// lines) that appear under any of the target "## " headings. A heading toggles
// the active section; a blank line or new bullet ends the current bullet; any
// new heading ends the section.
func collectBulletsUnderHeadings(content string, headings map[string]bool) []string {
	var bullets []string
	var cur []string
	inSection := false
	flush := func() {
		if len(cur) > 0 {
			bullets = append(bullets, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "## "):
			flush()
			inSection = headings[strings.ToLower(trimmed)]
			continue
		case strings.HasPrefix(trimmed, "# "):
			flush()
			inSection = false
			continue
		}
		if !inSection {
			continue
		}
		if trimmed == "" {
			flush()
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			flush()
			cur = append(cur, trimmed)
		} else if len(cur) > 0 {
			cur = append(cur, trimmed) // continuation of the current bullet
		}
	}
	flush()
	return bullets
}

// dispatchSignals harvests the durable outcome of a closed dispatch: its
// structured summary and any resolved decision, falling back to the latest
// freeform report. The dispatch id grounds it; its workspace is the context.
func dispatchSignals(disp *protocol.ChiefOfStaffDispatch) []notebook.DreamSignal {
	if disp == nil {
		return nil
	}
	title := strings.TrimSpace(disp.Label)
	if title == "" {
		title = strings.TrimSpace(disp.Brief)
	}
	ref := "dispatch:" + disp.ID
	ctx := "workspace:" + disp.WorkspaceID
	seen := strings.TrimSpace(disp.UpdatedAt)
	if disp.ReportedAt != nil && strings.TrimSpace(*disp.ReportedAt) != "" {
		seen = strings.TrimSpace(*disp.ReportedAt)
	}

	var texts []string
	if r := disp.StructuredReport; r != nil {
		if s := strings.TrimSpace(r.Summary); s != "" {
			texts = append(texts, s)
		}
		if decision := dispatchDecisionText(r.Request); decision != "" {
			texts = append(texts, decision)
		}
	}
	if len(texts) == 0 && disp.LatestReport != nil {
		if s := strings.TrimSpace(*disp.LatestReport); s != "" {
			texts = append(texts, s)
		}
	}

	out := make([]notebook.DreamSignal, 0, len(texts))
	for _, t := range texts {
		out = append(out, notebook.DreamSignal{
			Source:    notebook.SignalSourceDispatch,
			Title:     title,
			Text:      t,
			SourceRef: ref,
			Context:   ctx,
			Seen:      seen,
		})
	}
	return out
}

// dispatchDecisionText renders a resolved decision request as a single durable
// line ("Decision: <question> → <answer>"). An unanswered request carries no
// durable outcome yet, so it is skipped.
func dispatchDecisionText(req *protocol.DispatchDecisionRequest) string {
	if req == nil {
		return ""
	}
	question := strings.TrimSpace(req.Question)
	answer := ""
	if req.Response != nil {
		answer = strings.TrimSpace(*req.Response)
	}
	if answer == "" && req.Recommendation != nil {
		answer = strings.TrimSpace(*req.Recommendation)
	}
	if question == "" || answer == "" {
		return ""
	}
	return fmt.Sprintf("Decision: %s → %s", question, answer)
}

// dreamStatus returns a cheap summary of what a dream would consolidate now: the
// gate flag, candidate totals (overall and recurring-across-contexts), per-source
// counts, and the top candidates by durability signal.
func (d *Daemon) dreamStatus() (*protocol.NotebookDreamStatusResult, error) {
	set, err := d.harvestDreamCandidates()
	if err != nil {
		return nil, err
	}
	candidates := set.Candidates()
	res := &protocol.NotebookDreamStatusResult{
		Enabled:           parseBooleanSetting(d.store.GetSetting(SettingNotebookDreamingEnabled)),
		CandidateCount:    len(candidates),
		MultiContextCount: countMultiContext(candidates),
		SourceCounts:      sourceCounts(candidates),
		Top:               toProtocolDreamCandidates(candidates, topDreamCandidates),
	}
	return res, nil
}

// dreamRun performs a harvest and returns the candidate preview. apply is
// accepted for forward compatibility with the promote phase, but this phase is
// preview-only: it always reports Applied=false and writes nothing.
func (d *Daemon) dreamRun(apply bool) (*protocol.NotebookDreamRunResult, error) {
	set, err := d.harvestDreamCandidates()
	if err != nil {
		return nil, err
	}
	candidates := set.Candidates()
	res := &protocol.NotebookDreamRunResult{
		Applied:           false,
		CandidateCount:    len(candidates),
		MultiContextCount: countMultiContext(candidates),
		SourceCounts:      sourceCounts(candidates),
		Candidates:        toProtocolDreamCandidates(candidates, maxDreamRunCandidates),
	}
	return res, nil
}

func countMultiContext(candidates []notebook.DreamCandidate) int {
	n := 0
	for _, c := range candidates {
		if c.DistinctContexts() > 1 {
			n++
		}
	}
	return n
}

// sourceCounts tallies candidates by originating source, in a stable order.
func sourceCounts(candidates []notebook.DreamCandidate) []protocol.NotebookDreamSourceCount {
	counts := map[string]int{}
	for _, c := range candidates {
		counts[c.Source]++
	}
	sources := make([]string, 0, len(counts))
	for s := range counts {
		sources = append(sources, s)
	}
	sort.Strings(sources)
	out := make([]protocol.NotebookDreamSourceCount, 0, len(sources))
	for _, s := range sources {
		out = append(out, protocol.NotebookDreamSourceCount{Source: s, Count: counts[s]})
	}
	return out
}

// toProtocolDreamCandidates converts up to limit candidates to their protocol
// shape (limit <= 0 means all).
func toProtocolDreamCandidates(candidates []notebook.DreamCandidate, limit int) []protocol.NotebookDreamCandidate {
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]protocol.NotebookDreamCandidate, 0, len(candidates))
	for _, c := range candidates {
		pc := protocol.NotebookDreamCandidate{
			SignalKey:   c.SignalKey,
			Source:      c.Source,
			Snippet:     c.Snippet,
			Occurrences: c.Occurrences,
			Contexts:    c.Contexts,
			Sources:     c.Sources,
		}
		if c.Title != "" {
			pc.Title = protocol.Ptr(c.Title)
		}
		if c.FirstSeen != "" {
			pc.FirstSeen = protocol.Ptr(c.FirstSeen)
		}
		if c.LastSeen != "" {
			pc.LastSeen = protocol.Ptr(c.LastSeen)
		}
		out = append(out, pc)
	}
	return out
}

// handleNotebookDreamStatus serves `attn notebook dream status` over the unix
// socket (synchronous Response).
func (d *Daemon) handleNotebookDreamStatus(conn net.Conn) {
	res, err := d.dreamStatus()
	if err != nil {
		d.sendError(conn, "notebook dream status: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, NotebookDreamStatus: res})
}

// handleNotebookDreamRun serves `attn notebook dream [--dry-run]` over the unix
// socket. This phase is preview-only; the apply flag is reserved for the promote
// pass and currently never writes.
func (d *Daemon) handleNotebookDreamRun(conn net.Conn, msg *protocol.NotebookDreamRunMessage) {
	apply := false
	if msg.Apply != nil {
		apply = *msg.Apply
	}
	res, err := d.dreamRun(apply)
	if err != nil {
		d.sendError(conn, "notebook dream: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, NotebookDreamRun: res})
}
