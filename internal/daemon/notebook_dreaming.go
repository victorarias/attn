package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

// Dreaming — harvest + read-only inspection (phase one).
//
// This file implements the deterministic, LLM-free harvest and the inspection
// commands that surface it (`attn notebook dream status` / `--dry-run`). It scans
// the two surviving v1 sources — dated journals and closed chief-of-staff
// dispatches — into deduplicated candidates ordered by durability signal
// (recurrence across distinct contexts). (Workspace-context re-read, the former
// source-2, was removed once narration took ownership of distilling context.md
// into the journal.)
//
// The harvest functions here are read-only previews: they never write
// candidates.json and never touch durable memory. Persistence is the harvest_dream
// executor's job (harvestDreamExecutor, on the durable runner), dispatched nightly
// by the cron enqueuer; the gated LLM promote pass arrives in the follow-up PR.

// topDreamCandidates bounds how many candidates `dream status` returns inline.
const topDreamCandidates = 10

// maxDreamRunCandidates bounds how many candidates a `dream --dry-run` preview
// returns, so a large notebook can't produce an unbounded response.
const maxDreamRunCandidates = 200

// dreamHarvestUnion loads the persisted candidate set and merges a fresh harvest
// into it, returning the union and how many candidates were already persisted
// before the harvest. This is exactly what the next scheduled run would persist,
// so status and dry-run preview the real accumulated state — not just what one
// in-the-moment scan happens to see. It never writes; harvestDreamExecutor persists.
func (d *Daemon) dreamHarvestUnion() (set *notebook.DreamCandidateSet, persisted int, err error) {
	root, err := d.notebookRoot()
	if err != nil {
		return nil, 0, err
	}
	cands, err := notebook.LoadDreamCandidates(root)
	if err != nil {
		return nil, 0, err
	}
	set = notebook.LoadDreamCandidateSet(cands)
	persisted = set.Len()
	if err := d.harvestInto(set); err != nil {
		return nil, 0, err
	}
	return set, persisted, nil
}

// harvestInto scans the v1 sources into the supplied set, merging with whatever
// it already holds (re-adding a known source ref is idempotent).
//
// Source 2 (workspace-context Decisions/Constraints re-read) was removed when
// narration took ownership of distilling context.md into the journal: the journal
// is now the single durable system of record harvest reads from, so re-reading
// workspace context would double-count what narration already wrote.
func (d *Daemon) harvestInto(set *notebook.DreamCandidateSet) error {
	store, err := d.notebookStoreFor()
	if err != nil {
		return err
	}

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

	// 3. Closed dispatches: a dispatch is closed when its target session is gone.
	if d.store != nil {
		for _, disp := range d.store.ListChiefOfStaffDispatches("") {
			if disp == nil || d.store.Get(disp.SessionID) != nil {
				continue
			}
			for _, sig := range dispatchSignals(disp) {
				set.Add(sig)
			}
		}
	}

	return nil
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
// gate flag, the persisted-plus-fresh candidate totals (overall and recurring-
// across-contexts), per-source counts, the top candidates by durability signal,
// and the schedule bracketing the nightly runs.
func (d *Daemon) dreamStatus() (*protocol.NotebookDreamStatusResult, error) {
	set, persisted, err := d.dreamHarvestUnion()
	if err != nil {
		return nil, err
	}
	candidates := set.Candidates()
	res := &protocol.NotebookDreamStatusResult{
		Enabled:           d.dreamingEnabled(),
		CandidateCount:    len(candidates),
		MultiContextCount: countMultiContext(candidates),
		PersistedCount:    persisted,
		SourceCounts:      sourceCounts(candidates),
		Top:               toProtocolDreamCandidates(candidates, topDreamCandidates),
	}
	d.fillDreamSchedule(res)
	return res, nil
}

// fillDreamSchedule populates the schedule/timezone/last-run/next-run fields from
// the configured cron and persisted run state. A misconfigured frequency leaves
// the schedule fields unset rather than failing the whole status.
func (d *Daemon) fillDreamSchedule(res *protocol.NotebookDreamStatusResult) {
	sched, raw, err := d.dreamingSchedule()
	if err != nil {
		return
	}
	loc := d.dreamingLocation()
	res.Schedule = protocol.Ptr(raw)
	res.Timezone = protocol.Ptr(loc.String())

	root, err := d.notebookRoot()
	if err != nil {
		return
	}
	state, err := notebook.LoadDreamRunState(root)
	if err != nil {
		return
	}
	if state.LastRunAt != "" {
		res.LastRunAt = protocol.Ptr(state.LastRunAt)
	}
	// Next run is computed from the persisted anchor, or from now when dreaming
	// has never been anchored yet (its first run will land at the next slot).
	anchor, ok := parseDreamTime(state.ScheduledFrom)
	if !ok {
		anchor = time.Now()
	}
	res.NextRunAt = protocol.Ptr(sched.Next(anchor.In(loc)).UTC().Format(time.RFC3339))
}

// dreamRun performs a harvest and returns the candidate preview. apply is
// accepted for forward compatibility with the promote phase, but this phase is
// preview-only: it always reports Applied=false and writes nothing. The nightly
// scheduler is what persists; this command only previews the union.
func (d *Daemon) dreamRun(apply bool) (*protocol.NotebookDreamRunResult, error) {
	set, _, err := d.dreamHarvestUnion()
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
