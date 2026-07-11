package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/present"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// presentationToProto converts a store presentation to its protocol
// representation. It carries only the presentation row plus its
// already-enriched latest-round summary — never the manifest or comments,
// which are round-scoped and fetched separately.
func presentationToProto(p *store.Presentation) protocol.Presentation {
	out := protocol.Presentation{
		ID:                   p.ID,
		SessionID:            p.SessionID,
		Title:                p.Title,
		Kind:                 p.Kind,
		RepoPath:             p.RepoPath,
		Status:               p.Status,
		CreatedAt:            p.CreatedAt,
		LatestRoundSeq:       p.LatestRoundSeq,
		LatestRoundSubmitted: p.LatestRoundSubmitted,
	}
	if p.TicketID != nil {
		out.TicketID = p.TicketID
	}
	return out
}

func commentToProto(c *store.PresentationComment) protocol.PresentationComment {
	return protocol.PresentationComment{
		ID:        c.ID,
		RoundID:   c.RoundID,
		Filepath:  c.Filepath,
		LineStart: c.LineStart,
		LineEnd:   c.LineEnd,
		Side:      c.Side,
		Content:   c.Content,
		Author:    c.Author,
		CreatedAt: c.CreatedAt,
	}
}

// manifestToView converts a parsed manifest into the wire view sent to the
// app — the same shape whether it comes from a fresh present_open or a
// stored round's manifest_yaml. annotations is the resolved-annotations map
// (path -> resolved annotations); nil when resolution was not attempted or
// failed, in which case every file's Annotations is simply omitted.
func manifestToView(m *present.Manifest, annotations map[string][]present.ResolvedAnnotation) protocol.PresentManifestView {
	files := make([]protocol.PresentFile, len(m.Files))
	for i, f := range m.Files {
		pf := protocol.PresentFile{Path: f.Path}
		if f.Note != "" {
			pf.Note = protocol.Ptr(f.Note)
		}
		if resolved, ok := annotations[f.Path]; ok {
			pf.Annotations = make([]protocol.PresentAnnotation, len(resolved))
			for j, r := range resolved {
				pf.Annotations[j] = protocol.PresentAnnotation{
					LineStart: r.LineStart,
					LineEnd:   r.LineEnd,
					Comments:  r.Comments,
				}
			}
		}
		files[i] = pf
	}
	skip := m.Skip
	if skip == nil {
		skip = []string{}
	}
	view := protocol.PresentManifestView{
		Title: m.Title,
		Files: files,
		Skip:  skip,
	}
	if m.Summary != "" {
		view.Summary = protocol.Ptr(m.Summary)
	}
	return view
}

// roundToProto converts a store round to protocol, reparsing its stored
// manifest YAML to build the manifest view. The manifest was already
// validated at open time, so a parse failure here means stored data is
// corrupt, not a user input error. Annotations are re-resolved against the
// round's pinned head SHA in repoDir — deterministic, same architecture as
// the stats/changed-files progressive enhancements below. A resolution
// problem (unreadable file, broken anchor) never fails the round: the
// affected file's annotations are simply omitted from the view.
func roundToProto(r *store.PresentationRound, repoDir string) (*protocol.PresentationRound, error) {
	m, err := present.ParseManifest([]byte(r.ManifestYAML))
	if err != nil {
		return nil, fmt.Errorf("parse stored manifest for round %s: %w", r.ID, err)
	}
	annotations, _ := present.ResolveAnnotations(m, repoDir, r.HeadSHA)
	out := &protocol.PresentationRound{
		ID:             r.ID,
		PresentationID: r.PresentationID,
		Seq:            r.Seq,
		BaseSHA:        r.BaseSHA,
		HeadSHA:        r.HeadSHA,
		CreatedAt:      r.CreatedAt,
		Manifest:       manifestToView(m, annotations),
	}
	if r.SubmittedAt != nil {
		out.SubmittedAt = r.SubmittedAt
	}
	if r.Verdict != nil {
		out.Verdict = r.Verdict
	}
	return out, nil
}

// formatAnchorIssue renders an annotation resolution issue for display to the
// agent: "path[index]: message" for an issue tied to one annotation, or
// "path: message" for a file-level issue (index -1, e.g. unreadable content).
func formatAnchorIssue(issue present.AnchorIssue) string {
	if issue.Index < 0 {
		return fmt.Sprintf("%s: %s", issue.Path, issue.Message)
	}
	return fmt.Sprintf("%s[%d]: %s", issue.Path, issue.Index, issue.Message)
}

// handlePresentOpen opens a new presentation (or a new round on an existing
// one, when presentation_id is set) from a raw manifest YAML the agent wrote.
// The daemon is the sole authority for parsing and pinning: it never trusts a
// caller-supplied SHA or manifest shape.
func (d *Daemon) handlePresentOpen(conn net.Conn, msg *protocol.PresentOpenMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "present open: source_session_id is required")
		return
	}

	m, err := present.ParseManifest([]byte(msg.ManifestYaml))
	if err != nil {
		d.sendError(conn, "present open: "+err.Error())
		return
	}
	baseSHA, headSHA, err := present.Pin(m)
	if err != nil {
		d.sendError(conn, "present open: "+err.Error())
		return
	}

	_, issues := present.ResolveAnnotations(m, m.Frame.Repo, headSHA)
	var warnings []string
	var errMessages []string
	for _, issue := range issues {
		if issue.Warning {
			warnings = append(warnings, formatAnchorIssue(issue))
		} else {
			errMessages = append(errMessages, formatAnchorIssue(issue))
		}
	}
	if len(errMessages) > 0 {
		d.sendError(conn, "present open: annotation errors:\n"+strings.Join(errMessages, "\n"))
		return
	}

	now := time.Now()
	isNewPresentation := false
	var pres *store.Presentation

	if msg.PresentationID != nil && strings.TrimSpace(*msg.PresentationID) != "" {
		presentationID := strings.TrimSpace(*msg.PresentationID)
		existing, err := d.store.GetPresentation(presentationID)
		if err != nil {
			d.sendError(conn, "present open: unknown presentation "+presentationID)
			return
		}
		if existing.SessionID != sourceSessionID {
			d.sendError(conn, "present open: presentation "+presentationID+" does not belong to session "+sourceSessionID)
			return
		}
		pres = existing
	} else {
		var ticketID *string
		if msg.TicketID != nil && strings.TrimSpace(*msg.TicketID) != "" {
			t := strings.TrimSpace(*msg.TicketID)
			ticketID = &t
		} else if ticket, tErr := d.store.ActiveTicketForSession(sourceSessionID); tErr == nil && ticket != nil {
			ticketID = protocol.Ptr(ticket.ID)
		}
		created, err := d.store.CreatePresentation(sourceSessionID, ticketID, m.Title, m.Kind, m.Frame.Repo, now)
		if err != nil {
			d.sendError(conn, "present open: "+err.Error())
			return
		}
		pres = created
		isNewPresentation = true
	}

	round, err := d.store.CreatePresentationRound(pres.ID, msg.ManifestYaml, baseSHA, headSHA, now)
	if err != nil {
		d.sendError(conn, "present open: "+err.Error())
		return
	}

	result := &protocol.PresentOpenResult{
		PresentationID: pres.ID,
		RoundID:        round.ID,
		Seq:            round.Seq,
		BaseSHA:        baseSHA,
		HeadSHA:        headSHA,
		Title:          m.Title,
	}
	if len(warnings) > 0 {
		result.Warnings = warnings
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                true,
		PresentOpenResult: result,
	})

	// Re-fetch so the broadcast carries the fresh latest-round summary.
	if refreshed, err := d.store.GetPresentation(pres.ID); err == nil {
		proto := presentationToProto(refreshed)
		if isNewPresentation {
			d.broadcastMessage(protocol.PresentationAddedMessage{
				Event:        protocol.EventPresentationAdded,
				Presentation: proto,
			})
		} else {
			d.broadcastMessage(protocol.PresentationUpdatedMessage{
				Event:        protocol.EventPresentationUpdated,
				Presentation: proto,
			})
		}
	} else {
		d.logf("present open: failed to refresh presentation %s for broadcast: %v", pres.ID, err)
	}
}

// handlePresentFeedback reads a round's reviewer feedback back to the
// authoring agent as markdown. A round that has not been submitted yet still
// resolves — the markdown says so, so an agent can poll without erroring.
func (d *Daemon) handlePresentFeedback(conn net.Conn, msg *protocol.PresentFeedbackMessage) {
	presentationID := strings.TrimSpace(msg.PresentationID)
	if presentationID == "" {
		d.sendError(conn, "present feedback: presentation_id is required")
		return
	}
	pres, err := d.store.GetPresentation(presentationID)
	if err != nil {
		d.sendError(conn, "present feedback: unknown presentation "+presentationID)
		return
	}

	seq := 0
	if msg.Seq != nil {
		seq = *msg.Seq
	}
	round, err := d.store.GetPresentationRound(presentationID, seq)
	if err != nil {
		d.sendError(conn, "present feedback: "+err.Error())
		return
	}

	comments, err := d.store.ListPresentationComments(round.ID)
	if err != nil {
		d.sendError(conn, "present feedback: "+err.Error())
		return
	}
	feedbackComments := make([]present.FeedbackComment, len(comments))
	for i, c := range comments {
		feedbackComments[i] = present.FeedbackComment{
			Filepath:  c.Filepath,
			LineStart: c.LineStart,
			LineEnd:   c.LineEnd,
			Side:      c.Side,
			Content:   c.Content,
		}
	}

	submittedAt := ""
	if round.SubmittedAt != nil {
		submittedAt = *round.SubmittedAt
	}
	verdict := ""
	if round.Verdict != nil {
		verdict = *round.Verdict
	}
	markdown := present.RenderFeedback(pres.RepoPath, pres.Title, round.Seq, round.BaseSHA, round.HeadSHA, submittedAt, verdict, feedbackComments)
	// A reviewer can close a presentation without ever reviewing this round —
	// surface that explicitly so a polling agent learns the review isn't
	// coming, rather than polling "not submitted yet" forever.
	if pres.Status == "closed" && round.SubmittedAt == nil {
		markdown += "\nPresentation closed without review.\n"
	}

	var verdictPtr *string
	if verdict != "" {
		verdictPtr = &verdict
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		PresentFeedbackResult: &protocol.PresentFeedbackResult{
			Markdown:           markdown,
			Seq:                round.Seq,
			Submitted:          round.SubmittedAt != nil,
			Verdict:            verdictPtr,
			PresentationStatus: pres.Status,
		},
	})
}

// handleGetPresentations reads the full list of presentations, the way the
// app renders the present surface.
func (d *Daemon) handleGetPresentations(client *wsClient, msg *protocol.GetPresentationsMessage) {
	result := protocol.GetPresentationsResultMessage{
		Event:   protocol.EventGetPresentationsResult,
		Success: false,
	}

	list, err := d.store.ListPresentations()
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Presentations = make([]protocol.Presentation, len(list))
	for i, p := range list {
		result.Presentations[i] = presentationToProto(p)
	}
	result.Success = true
	d.sendToClient(client, result)
}

// handleGetPresentationRound fetches one round of a presentation — the round
// record plus its comments — for the detail view.
func (d *Daemon) handleGetPresentationRound(client *wsClient, msg *protocol.GetPresentationRoundMessage) {
	result := protocol.GetPresentationRoundResultMessage{
		Event:   protocol.EventGetPresentationRoundResult,
		Success: false,
	}

	presentationID := strings.TrimSpace(msg.PresentationID)
	pres, err := d.store.GetPresentation(presentationID)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	seq := 0
	if msg.Seq != nil {
		seq = *msg.Seq
	}
	round, err := d.store.GetPresentationRound(presentationID, seq)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	comments, err := d.store.ListPresentationComments(round.ID)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	protoRound, err := roundToProto(round, pres.RepoPath)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	protoPres := presentationToProto(pres)
	result.Presentation = &protoPres
	result.Round = protoRound
	result.Comments = make([]protocol.PresentationComment, len(comments))
	for i, c := range comments {
		result.Comments[i] = commentToProto(c)
	}

	// Best-effort drift signal: the repo may have moved on since the round
	// was pinned. A rev-parse failure (repo gone, etc.) is non-fatal — just
	// omit the field.
	if headSHA, err := attngit.Output(attngit.OpMetadata, pres.RepoPath, "rev-parse", "HEAD"); err == nil {
		result.RepoHeadSHA = protocol.Ptr(strings.TrimSpace(string(headSHA)))
	}

	// Per-file ± line stats are a progressive enhancement for the rail: a
	// lookup failure or empty result must never fail the round fetch.
	stats := d.presentFileStats(pres.RepoPath, round.BaseSHA, round.HeadSHA)
	if len(stats) > 0 {
		for i := range result.Round.Manifest.Files {
			path := result.Round.Manifest.Files[i].Path
			if s, ok := stats[path]; ok {
				result.Round.Manifest.Files[i].Additions = protocol.Ptr(s[0])
				result.Round.Manifest.Files[i].Deletions = protocol.Ptr(s[1])
			}
		}
	}

	// The full changed-file list (tour + other) is a progressive enhancement
	// too: a git error leaves ChangedFiles nil and the round still loads.
	if changed, err := d.presentChangedFiles(pres.RepoPath, round.BaseSHA, round.HeadSHA, stats); err == nil {
		result.Round.ChangedFiles = changed
	}

	result.Success = true
	d.sendToClient(client, result)
}

// presentFileStats returns path -> [additions, deletions] for the pinned
// base..head diff, from `git diff --numstat`. Binary files (numstat "-") are
// omitted. Rename lines are skipped — the numstat rename encoding
// ("old => new" or "{old => new}/tail") doesn't cleanly resolve to a single
// manifest path, and leaving those files stats-less is an accepted
// limitation. Errors return nil; stats are a progressive enhancement and
// must never fail a round fetch.
func (d *Daemon) presentFileStats(repoDir, baseSHA, headSHA string) map[string][2]int {
	out, err := attngit.Output(attngit.OpDiff, repoDir, "diff", "--numstat", baseSHA+".."+headSHA)
	if err != nil {
		return nil
	}
	return parsePresentNumstat(string(out))
}

// presentChangedFiles lists every path changed between the round's pinned
// base..head SHAs, for the frontend to derive the Tour/Other/Skipped rail
// groups from (paths already named in the manifest are included too — the
// frontend does the set subtraction). stats is the same numstat map used for
// manifest file stats, reused here so numstat only runs once per round fetch.
func (d *Daemon) presentChangedFiles(repoDir, baseSHA, headSHA string, stats map[string][2]int) ([]protocol.PresentFile, error) {
	out, err := attngit.Output(attngit.OpDiff, repoDir, "diff", "--name-only", "-z", baseSHA+".."+headSHA)
	if err != nil {
		return nil, err
	}
	var files []protocol.PresentFile
	for _, path := range strings.Split(string(out), "\x00") {
		if path == "" {
			continue
		}
		pf := protocol.PresentFile{Path: path}
		if s, ok := stats[path]; ok {
			pf.Additions = protocol.Ptr(s[0])
			pf.Deletions = protocol.Ptr(s[1])
		}
		files = append(files, pf)
	}
	return files, nil
}

// parsePresentNumstat parses `git diff --numstat` output into path ->
// [additions, deletions]. Lines are tab-separated: additions, deletions,
// path. Binary files report "-" for both counts and are omitted. Rename
// lines carry a path field containing " => " (optionally with a "{old =>
// new}" brace segment) and are skipped rather than guessed at.
func parsePresentNumstat(output string) map[string][2]int {
	result := make(map[string][2]int)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[0] == "-" || parts[1] == "-" {
			// Binary file: no line stats available.
			continue
		}
		additions, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		deletions, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		path := parts[2]
		if strings.Contains(path, " => ") {
			// Rename line — accepted limitation, see doc comment.
			continue
		}
		result[path] = [2]int{additions, deletions}
	}
	return result
}

// handlePresentSubmitRound hands a round's review back to the authoring
// agent: it validates and stores the comments, marks the round submitted, and
// — when handback is set — wakes the authoring agent up, either through a
// ticket comment (ticket-bound presentations) or a direct doorbell (bare
// sessions, best-effort: skipped, not queued, if the session is not idle).
func (d *Daemon) handlePresentSubmitRound(client *wsClient, msg *protocol.PresentSubmitRoundMessage) {
	result := protocol.PresentSubmitRoundResultMessage{
		Event:   protocol.EventPresentSubmitRoundResult,
		RoundID: msg.RoundID,
		Success: false,
	}

	roundID := strings.TrimSpace(msg.RoundID)
	if roundID == "" {
		result.Error = protocol.Ptr("round_id is required")
		d.sendToClient(client, result)
		return
	}

	if msg.Verdict != "approved" && msg.Verdict != "feedback" {
		result.Error = protocol.Ptr(fmt.Sprintf("verdict must be \"approved\" or \"feedback\", got %q", msg.Verdict))
		d.sendToClient(client, result)
		return
	}

	comments := make([]store.PresentationComment, 0, len(msg.Comments))
	for i, c := range msg.Comments {
		if strings.TrimSpace(c.Filepath) == "" {
			result.Error = protocol.Ptr(fmt.Sprintf("comments[%d].filepath is required", i))
			d.sendToClient(client, result)
			return
		}
		if c.Side != "new" && c.Side != "old" {
			result.Error = protocol.Ptr(fmt.Sprintf("comments[%d].side must be \"new\" or \"old\", got %q", i, c.Side))
			d.sendToClient(client, result)
			return
		}
		if c.LineStart < 1 {
			result.Error = protocol.Ptr(fmt.Sprintf("comments[%d].line_start must be >= 1", i))
			d.sendToClient(client, result)
			return
		}
		if c.LineEnd < c.LineStart {
			result.Error = protocol.Ptr(fmt.Sprintf("comments[%d].line_end must be >= line_start", i))
			d.sendToClient(client, result)
			return
		}
		if strings.TrimSpace(c.Content) == "" {
			result.Error = protocol.Ptr(fmt.Sprintf("comments[%d].content is required", i))
			d.sendToClient(client, result)
			return
		}
		comments = append(comments, store.PresentationComment{
			Filepath:  c.Filepath,
			LineStart: c.LineStart,
			LineEnd:   c.LineEnd,
			Side:      c.Side,
			Content:   c.Content,
			Author:    "user",
		})
	}

	now := time.Now()
	if err := d.store.SubmitPresentationRound(roundID, msg.Verdict, comments, now); err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Success = true
	d.sendToClient(client, result)

	round, err := d.store.GetPresentationRoundByID(roundID)
	if err != nil {
		d.logf("present submit round: failed to reload round %s after submit: %v", roundID, err)
		return
	}
	pres, err := d.store.GetPresentation(round.PresentationID)
	if err != nil {
		d.logf("present submit round: failed to load presentation %s after submit: %v", round.PresentationID, err)
		return
	}

	proto := presentationToProto(pres)
	d.broadcastMessage(protocol.PresentationUpdatedMessage{
		Event:        protocol.EventPresentationUpdated,
		Presentation: proto,
	})

	if !msg.Handback {
		return
	}
	d.handbackPresentationRound(pres, round.Seq, msg.Verdict)
}

// handbackPresentationRound wakes the authoring agent once a round has been
// submitted. Ticket-bound presentations get a durable ticket comment (queued
// regardless of the session's state); bare sessions get a best-effort direct
// doorbell that is silently skipped when the session is not idle — chunk-1's
// accepted limitation, since a bare presentation has no durable inbox to fall
// back on. verdict is verdict-aware wording: "approved" tells the agent the
// round was approved (possibly with nits); "feedback" keeps the original
// "submitted" wording.
func (d *Daemon) handbackPresentationRound(pres *store.Presentation, seq int, verdict string) {
	var notice string
	if verdict == "approved" {
		notice = fmt.Sprintf("Present round %d of %q approved — run `attn present feedback %s`", seq, pres.Title, pres.ID)
	} else {
		notice = fmt.Sprintf("Present round %d of %q submitted — run `attn present feedback %s`", seq, pres.Title, pres.ID)
	}

	if pres.TicketID != nil && strings.TrimSpace(*pres.TicketID) != "" {
		ticketID := strings.TrimSpace(*pres.TicketID)
		_, err := d.store.AddTicketComment(ticketID, "attn", notice, time.Now())
		d.afterTicketMutation(ticketID, err)
		if err != nil {
			d.logf("present handback: failed to comment on ticket %s: %v", ticketID, err)
		}
		return
	}

	session := d.store.Get(pres.SessionID)
	if session == nil || !isNudgeDeliveryAllowed(string(session.State)) {
		d.logf("present handback: session %s is waiting for approval, skipping doorbell for presentation %s", pres.SessionID, pres.ID)
		return
	}
	if err := d.typeDoorbell(pres.SessionID, "\U0001F4FD "+notice+"."); err != nil {
		d.logf("present handback: doorbell failed for session %s: %v", pres.SessionID, err)
	}
}

// handlePresentClose dismisses a presentation without a review: the
// presentation's status moves straight to "closed". Unlike
// handlePresentSubmitRound, there is no round submission and no handback —
// the reviewer is declining to review, not handing feedback back.
func (d *Daemon) handlePresentClose(client *wsClient, msg *protocol.PresentCloseMessage) {
	result := protocol.PresentCloseResultMessage{
		Event:          protocol.EventPresentCloseResult,
		PresentationID: msg.PresentationID,
		Success:        false,
	}

	presentationID := strings.TrimSpace(msg.PresentationID)
	if presentationID == "" {
		result.Error = protocol.Ptr("presentation_id is required")
		d.sendToClient(client, result)
		return
	}

	if err := d.store.ClosePresentation(presentationID, time.Now()); err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Success = true
	d.sendToClient(client, result)

	pres, err := d.store.GetPresentation(presentationID)
	if err != nil {
		d.logf("present close: failed to reload presentation %s after close: %v", presentationID, err)
		return
	}
	d.broadcastMessage(protocol.PresentationUpdatedMessage{
		Event:        protocol.EventPresentationUpdated,
		Presentation: presentationToProto(pres),
	})
}
