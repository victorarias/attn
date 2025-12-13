# PR Detail Refresh Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement smart PR detail refresh with heat states (hot/warm/cold) that balances CI status freshness with API quota efficiency.

**Architecture:** Add heat_state tracking to PRs in SQLite. After each list poll, run a detail refresh pass that checks each visible PR's heat state and elapsed time. User interactions (click, approve, unmute) trigger immediate detail fetch and set PR to hot.

**Tech Stack:** Go (daemon), SQLite (storage), TypeScript/React (frontend)

---

## Task 1: Add Heat State Columns to Database Schema

**Files:**
- Modify: `internal/store/sqlite.go:11-60` (schema)
- Modify: `internal/store/sqlite.go:92-114` (migrations)

**Step 1: Update schema constant**

In `internal/store/sqlite.go`, add columns to the `prs` table definition:

```go
const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	label TEXT NOT NULL,
	directory TEXT NOT NULL,
	state TEXT NOT NULL DEFAULT 'idle',
	state_since TEXT NOT NULL,
	state_updated_at TEXT NOT NULL,
	todos TEXT,
	last_seen TEXT NOT NULL,
	muted INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS prs (
	id TEXT PRIMARY KEY,
	repo TEXT NOT NULL,
	number INTEGER NOT NULL,
	title TEXT NOT NULL,
	url TEXT NOT NULL,
	role TEXT NOT NULL,
	state TEXT NOT NULL,
	reason TEXT,
	last_updated TEXT NOT NULL,
	last_polled TEXT NOT NULL,
	muted INTEGER NOT NULL DEFAULT 0,
	details_fetched INTEGER NOT NULL DEFAULT 0,
	details_fetched_at TEXT,
	mergeable INTEGER,
	mergeable_state TEXT,
	ci_status TEXT,
	review_status TEXT,
	head_sha TEXT,
	comment_count INTEGER NOT NULL DEFAULT 0,
	approved_by_me INTEGER NOT NULL DEFAULT 0,
	heat_state TEXT NOT NULL DEFAULT 'cold',
	last_heat_activity_at TEXT
);

CREATE TABLE IF NOT EXISTS repos (
	repo TEXT PRIMARY KEY,
	muted INTEGER NOT NULL DEFAULT 0,
	collapsed INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS pr_interactions (
	pr_id TEXT PRIMARY KEY,
	last_visited_at TEXT,
	last_approved_at TEXT,
	last_seen_sha TEXT,
	last_seen_comment_count INTEGER,
	last_seen_ci_status TEXT
);
`
```

**Step 2: Add migrations for existing databases**

Add these migrations to the `migrations` slice in `migrateDB`:

```go
func migrateDB(db *sql.DB) error {
	migrations := []struct {
		check string
		alter string
	}{
		{"SELECT head_sha FROM prs LIMIT 1", "ALTER TABLE prs ADD COLUMN head_sha TEXT"},
		{"SELECT comment_count FROM prs LIMIT 1", "ALTER TABLE prs ADD COLUMN comment_count INTEGER NOT NULL DEFAULT 0"},
		{"SELECT approved_by_me FROM prs LIMIT 1", "ALTER TABLE prs ADD COLUMN approved_by_me INTEGER NOT NULL DEFAULT 0"},
		{"SELECT heat_state FROM prs LIMIT 1", "ALTER TABLE prs ADD COLUMN heat_state TEXT NOT NULL DEFAULT 'cold'"},
		{"SELECT last_heat_activity_at FROM prs LIMIT 1", "ALTER TABLE prs ADD COLUMN last_heat_activity_at TEXT"},
		{"SELECT last_seen_ci_status FROM pr_interactions LIMIT 1", "ALTER TABLE pr_interactions ADD COLUMN last_seen_ci_status TEXT"},
	}

	for _, m := range migrations {
		_, err := db.Exec(m.check)
		if err != nil {
			if _, err := db.Exec(m.alter); err != nil {
				return err
			}
		}
	}

	return nil
}
```

**Step 3: Verify migration runs**

```bash
rm -f ~/.attn/attn.db && go test ./internal/store -run TestOpenDB -v
```

Expected: PASS (or create a simple test if none exists)

**Step 4: Commit**

```bash
git add internal/store/sqlite.go
git commit -m "feat(store): add heat_state columns to prs table"
```

---

## Task 2: Add Heat State Constants and PR Fields

**Files:**
- Modify: `internal/protocol/types.go:54-73` (constants)
- Modify: `internal/protocol/types.go:236-260` (PR struct)

**Step 1: Add heat state constants**

After the PR roles constants, add:

```go
// PR heat states (for detail refresh scheduling)
const (
	HeatStateHot  = "hot"
	HeatStateWarm = "warm"
	HeatStateCold = "cold"
)

// Heat state timing constants
const (
	HeatHotDuration     = 3 * time.Minute   // Stay hot for 3 min after activity
	HeatWarmDuration    = 10 * time.Minute  // Stay warm for 10 min total
	HeatHotInterval     = 30 * time.Second  // Refresh hot PRs every 30s
	HeatWarmInterval    = 2 * time.Minute   // Refresh warm PRs every 2 min
	HeatColdInterval    = 10 * time.Minute  // Refresh cold PRs every 10 min
)
```

**Step 2: Add fields to PR struct**

Add these fields to the PR struct after `HasNewChanges`:

```go
type PR struct {
	// ... existing fields ...
	HasNewChanges bool   `json:"has_new_changes"` // true if PR changed since last visit
	// Heat state for detail refresh scheduling
	HeatState          string    `json:"heat_state"`           // hot, warm, cold
	LastHeatActivityAt time.Time `json:"last_heat_activity_at"` // when heat was last triggered
}
```

**Step 3: Commit**

```bash
git add internal/protocol/types.go
git commit -m "feat(protocol): add heat state constants and PR fields"
```

---

## Task 3: Update Store to Read/Write Heat State

**Files:**
- Modify: `internal/store/store.go:298-410` (SetPRs)
- Modify: `internal/store/store.go:438-473` (ListPRs)
- Modify: `internal/store/store.go:740-777` (scanPR)

**Step 1: Update SetPRs to preserve heat state**

In the `SetPRs` method, update the existing PR query to include heat fields:

```go
rows, err := s.db.Query(`SELECT id, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, comment_count, approved_by_me, heat_state, last_heat_activity_at FROM prs`)
```

Update the scan in the loop:

```go
var heatState sql.NullString
var lastHeatActivityAt sql.NullString
rows.Scan(&pr.ID, &muted, &detailsFetched, &detailsFetchedAt, &mergeable, &mergeableState, &ciStatus, &reviewStatus, &headSHA, &commentCount, &approvedByMe, &heatState, &lastHeatActivityAt)
// ... existing assignments ...
pr.HeatState = heatState.String
if pr.HeatState == "" {
	pr.HeatState = protocol.HeatStateCold
}
if lastHeatActivityAt.Valid {
	pr.LastHeatActivityAt, _ = time.Parse(time.RFC3339, lastHeatActivityAt.String)
}
```

In the preservation logic, add:

```go
if ex, ok := existing[pr.ID]; ok {
	// ... existing preservation ...
	// Preserve heat state
	if pr.HeatState == "" || pr.HeatState == protocol.HeatStateCold {
		pr.HeatState = ex.HeatState
		pr.LastHeatActivityAt = ex.LastHeatActivityAt
	}
}
```

Update the INSERT statement:

```go
s.db.Exec(`
	INSERT INTO prs (id, repo, number, title, url, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, comment_count, approved_by_me, heat_state, last_heat_activity_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	pr.ID, pr.Repo, pr.Number, pr.Title, pr.URL, pr.Role, pr.State, pr.Reason,
	pr.LastUpdated.Format(time.RFC3339), pr.LastPolled.Format(time.RFC3339),
	boolToInt(pr.Muted), boolToInt(pr.DetailsFetched), nullTimeString(pr.DetailsFetchedAt),
	mergeableVal, nullString(pr.MergeableState), nullString(pr.CIStatus), nullString(pr.ReviewStatus),
	nullString(pr.HeadSHA), pr.CommentCount, boolToInt(pr.ApprovedByMe),
	nullString(pr.HeatState), nullTimeString(pr.LastHeatActivityAt),
)
```

**Step 2: Update scanPR to read heat state**

Update `scanPR` function to include heat fields:

```go
func scanPR(rows *sql.Rows) *protocol.PR {
	var pr protocol.PR
	var muted, detailsFetched, approvedByMe int
	var lastUpdated, lastPolled string
	var detailsFetchedAt, mergeableState, ciStatus, reviewStatus, headSHA sql.NullString
	var heatState, lastHeatActivityAt sql.NullString
	var mergeable sql.NullInt64
	var commentCount int

	err := rows.Scan(
		&pr.ID, &pr.Repo, &pr.Number, &pr.Title, &pr.URL, &pr.Role, &pr.State, &pr.Reason,
		&lastUpdated, &lastPolled, &muted, &detailsFetched, &detailsFetchedAt,
		&mergeable, &mergeableState, &ciStatus, &reviewStatus,
		&headSHA, &commentCount, &approvedByMe,
		&heatState, &lastHeatActivityAt,
	)
	if err != nil {
		return nil
	}

	// ... existing assignments ...
	pr.HeatState = heatState.String
	if pr.HeatState == "" {
		pr.HeatState = protocol.HeatStateCold
	}
	if lastHeatActivityAt.Valid {
		pr.LastHeatActivityAt, _ = time.Parse(time.RFC3339, lastHeatActivityAt.String)
	}

	return &pr
}
```

**Step 3: Update all SELECT queries to include heat columns**

Update these queries in store.go:
- `ListPRs` (line ~451)
- `ListPRsByRepo` (line ~529)
- `GetPR` (line ~496)

Add `, heat_state, last_heat_activity_at` to each SELECT.

**Step 4: Update scanPRRow similarly**

Same changes as scanPR but for the Row version.

**Step 5: Run tests**

```bash
go test ./internal/store -v
```

Expected: All tests pass

**Step 6: Commit**

```bash
git add internal/store/store.go
git commit -m "feat(store): read/write heat state for PRs"
```

---

## Task 4: Add Store Methods for Heat Management

**Files:**
- Modify: `internal/store/store.go` (add new methods after MarkPRApproved)

**Step 1: Add SetPRHot method**

```go
// SetPRHot sets a PR to hot state and updates last activity time
func (s *Store) SetPRHot(prID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	now := time.Now().Format(time.RFC3339)
	s.db.Exec(`UPDATE prs SET heat_state = ?, last_heat_activity_at = ? WHERE id = ?`,
		protocol.HeatStateHot, now, prID)
}
```

**Step 2: Add DecayHeatStates method**

```go
// DecayHeatStates transitions PRs from hot→warm→cold based on elapsed time
func (s *Store) DecayHeatStates() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	now := time.Now()
	warmThreshold := now.Add(-protocol.HeatHotDuration).Format(time.RFC3339)
	coldThreshold := now.Add(-protocol.HeatWarmDuration).Format(time.RFC3339)

	// Hot → Warm (after 3 min)
	s.db.Exec(`UPDATE prs SET heat_state = ? WHERE heat_state = ? AND last_heat_activity_at < ?`,
		protocol.HeatStateWarm, protocol.HeatStateHot, warmThreshold)

	// Warm → Cold (after 10 min)
	s.db.Exec(`UPDATE prs SET heat_state = ? WHERE heat_state = ? AND last_heat_activity_at < ?`,
		protocol.HeatStateCold, protocol.HeatStateWarm, coldThreshold)
}
```

**Step 3: Add GetPRsNeedingDetailRefresh method**

```go
// GetPRsNeedingDetailRefresh returns visible PRs that need detail refresh based on heat state
func (s *Store) GetPRsNeedingDetailRefresh() []*protocol.PR {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	// Get muted repos
	mutedRepos := make(map[string]bool)
	repoRows, _ := s.db.Query("SELECT repo FROM repos WHERE muted = 1")
	if repoRows != nil {
		defer repoRows.Close()
		for repoRows.Next() {
			var repo string
			repoRows.Scan(&repo)
			mutedRepos[repo] = true
		}
	}

	now := time.Now()
	var result []*protocol.PR

	rows, err := s.db.Query(`
		SELECT id, repo, number, title, url, role, state, reason, last_updated, last_polled,
		       muted, details_fetched, details_fetched_at, mergeable, mergeable_state,
		       ci_status, review_status, head_sha, comment_count, approved_by_me,
		       heat_state, last_heat_activity_at
		FROM prs
		WHERE muted = 0`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		pr := scanPR(rows)
		if pr == nil {
			continue
		}

		// Skip muted repos
		if mutedRepos[pr.Repo] {
			continue
		}

		// Check if refresh needed based on heat state
		elapsed := now.Sub(pr.DetailsFetchedAt)
		needsRefresh := false

		switch pr.HeatState {
		case protocol.HeatStateHot:
			needsRefresh = elapsed > protocol.HeatHotInterval
		case protocol.HeatStateWarm:
			needsRefresh = elapsed > protocol.HeatWarmInterval
		default: // cold
			needsRefresh = elapsed > protocol.HeatColdInterval
		}

		// Also refresh if details were never fetched
		if !pr.DetailsFetched {
			needsRefresh = true
		}

		if needsRefresh {
			result = append(result, pr)
		}
	}

	return result
}
```

**Step 4: Run tests**

```bash
go test ./internal/store -v
```

**Step 5: Commit**

```bash
git add internal/store/store.go
git commit -m "feat(store): add heat state management methods"
```

---

## Task 5: Update UpdatePRDetails to Store HeadSHA

**Files:**
- Modify: `internal/store/store.go:500-518` (UpdatePRDetails)

**Step 1: Update UpdatePRDetails signature and implementation**

```go
// UpdatePRDetails updates the detail fields for a PR
func (s *Store) UpdatePRDetails(id string, mergeable *bool, mergeableState, ciStatus, reviewStatus, headSHA string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	var mergeableVal *int
	if mergeable != nil {
		v := boolToInt(*mergeable)
		mergeableVal = &v
	}

	now := time.Now().Format(time.RFC3339)
	s.db.Exec(`UPDATE prs SET details_fetched = 1, details_fetched_at = ?, mergeable = ?, mergeable_state = ?, ci_status = ?, review_status = ?, head_sha = ? WHERE id = ?`,
		now, mergeableVal, mergeableState, ciStatus, reviewStatus, headSHA, id)
}
```

**Step 2: Update caller in daemon.go**

In `internal/daemon/daemon.go`, update the call to `UpdatePRDetails`:

```go
d.store.UpdatePRDetails(pr.ID, details.Mergeable, details.MergeableState, details.CIStatus, details.ReviewStatus, details.HeadSHA)
```

**Step 3: Run tests**

```bash
go test ./internal/... -v
```

**Step 4: Commit**

```bash
git add internal/store/store.go internal/daemon/daemon.go
git commit -m "feat(store): include headSHA in UpdatePRDetails"
```

---

## Task 6: Add Detail Refresh to Daemon Poll Cycle

**Files:**
- Modify: `internal/daemon/daemon.go:552-576` (doPRPoll)

**Step 1: Create doDetailRefresh method**

Add after `doPRPoll`:

```go
// doDetailRefresh fetches details for PRs that need refresh based on heat state
func (d *Daemon) doDetailRefresh() {
	if d.ghClient == nil || !d.ghClient.IsAvailable() {
		return
	}

	// First decay heat states
	d.store.DecayHeatStates()

	// Get PRs needing refresh
	prs := d.store.GetPRsNeedingDetailRefresh()
	if len(prs) == 0 {
		return
	}

	d.logf("Detail refresh: %d PRs need refresh", len(prs))

	refreshedCount := 0
	for _, pr := range prs {
		details, err := d.ghClient.FetchPRDetails(pr.Repo, pr.Number)
		if err != nil {
			d.logf("Failed to fetch details for %s: %v", pr.ID, err)
			continue
		}

		// Check if SHA changed (new commits) - triggers hot state
		if pr.HeadSHA != "" && details.HeadSHA != pr.HeadSHA {
			d.store.SetPRHot(pr.ID)
		}

		d.store.UpdatePRDetails(pr.ID, details.Mergeable, details.MergeableState, details.CIStatus, details.ReviewStatus, details.HeadSHA)
		refreshedCount++
	}

	if refreshedCount > 0 {
		d.logf("Detail refresh: updated %d PRs", refreshedCount)
		// Broadcast updated PRs
		d.broadcastPRs()
	}
}
```

**Step 2: Call doDetailRefresh after doPRPoll**

Update `doPRPoll` to call detail refresh:

```go
func (d *Daemon) doPRPoll() {
	prs, err := d.ghClient.FetchAll()
	if err != nil {
		d.logf("PR poll error: %v", err)
		return
	}

	d.store.SetPRs(prs)

	// Broadcast to WebSocket clients
	allPRs := d.store.ListPRs("")
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventPRsUpdated,
		PRs:   allPRs,
	})

	// Count waiting (non-muted) PRs for logging
	waiting := 0
	for _, pr := range allPRs {
		if pr.State == protocol.StateWaiting && !pr.Muted {
			waiting++
		}
	}
	d.logf("PR poll: %d PRs (%d waiting)", len(prs), waiting)

	// Run detail refresh after list poll
	d.doDetailRefresh()
}
```

**Step 3: Run tests**

```bash
go test ./internal/daemon -v
```

**Step 4: Commit**

```bash
git add internal/daemon/daemon.go
git commit -m "feat(daemon): run detail refresh after list poll with heat decay"
```

---

## Task 7: Trigger Immediate Detail Fetch on User Interactions

**Files:**
- Modify: `internal/daemon/websocket.go:258-299` (handleClientMessage)

**Step 1: Add helper method for immediate detail fetch**

Add to daemon.go:

```go
// fetchPRDetailsImmediate fetches details for a single PR immediately and sets it hot
func (d *Daemon) fetchPRDetailsImmediate(prID string) {
	if d.ghClient == nil || !d.ghClient.IsAvailable() {
		return
	}

	pr := d.store.GetPR(prID)
	if pr == nil {
		return
	}

	// Skip if muted
	if pr.Muted {
		return
	}
	// Skip if repo is muted
	repoState := d.store.GetRepoState(pr.Repo)
	if repoState != nil && repoState.Muted {
		return
	}

	d.store.SetPRHot(prID)

	details, err := d.ghClient.FetchPRDetails(pr.Repo, pr.Number)
	if err != nil {
		d.logf("Immediate fetch failed for %s: %v", prID, err)
		return
	}

	d.store.UpdatePRDetails(prID, details.Mergeable, details.MergeableState, details.CIStatus, details.ReviewStatus, details.HeadSHA)
	d.logf("Immediate fetch complete for %s (heat=hot)", prID)
}
```

**Step 2: Trigger on pr_visited**

Update the `CmdPRVisited` handler in websocket.go:

```go
case protocol.CmdPRVisited:
	visitedMsg := msg.(*protocol.PRVisitedMessage)
	d.logf("Marking PR %s as visited", visitedMsg.ID)
	d.store.MarkPRVisited(visitedMsg.ID)
	d.store.SetPRHot(visitedMsg.ID)
	go d.fetchPRDetailsImmediate(visitedMsg.ID)
	d.broadcastPRs()
```

**Step 3: Trigger on approve success**

Update the `MsgApprovePR` handler - the SetPRHot and immediate fetch are already triggered by MarkPRApproved, but add explicit immediate fetch:

```go
case protocol.MsgApprovePR:
	appMsg := msg.(*protocol.ApprovePRMessage)
	d.logf("Processing approve for %s#%d", appMsg.Repo, appMsg.Number)
	go func() {
		err := d.ghClient.ApprovePR(appMsg.Repo, appMsg.Number)
		result := protocol.PRActionResultMessage{
			Event:   protocol.MsgPRActionResult,
			Action:  "approve",
			Repo:    appMsg.Repo,
			Number:  appMsg.Number,
			Success: err == nil,
		}
		if err != nil {
			result.Error = err.Error()
			d.logf("Approve failed for %s#%d: %v", appMsg.Repo, appMsg.Number, err)
		} else {
			d.logf("Approve succeeded for %s#%d", appMsg.Repo, appMsg.Number)
			prID := fmt.Sprintf("%s#%d", appMsg.Repo, appMsg.Number)
			d.store.MarkPRApproved(prID)
			d.store.SetPRHot(prID)
			go d.fetchPRDetailsImmediate(prID)
		}
		d.sendToClient(client, result)
		d.logf("Sent approve result to client")
		d.RefreshPRs()
	}()
```

**Step 4: Trigger on unmute**

Update `CmdMutePR` handler:

```go
case protocol.CmdMutePR:
	muteMsg := msg.(*protocol.MutePRMessage)
	// Check if we're unmuting (PR was muted before)
	pr := d.store.GetPR(muteMsg.ID)
	wasMuted := pr != nil && pr.Muted

	d.store.ToggleMutePR(muteMsg.ID)

	// If unmuting, set hot and fetch details
	if wasMuted {
		d.store.SetPRHot(muteMsg.ID)
		go d.fetchPRDetailsImmediate(muteMsg.ID)
	}
	d.broadcastPRs()
```

Update `CmdMuteRepo` handler:

```go
case protocol.CmdMuteRepo:
	muteMsg := msg.(*protocol.MuteRepoMessage)
	// Check if we're unmuting
	repoState := d.store.GetRepoState(muteMsg.Repo)
	wasMuted := repoState != nil && repoState.Muted

	d.store.ToggleMuteRepo(muteMsg.Repo)

	// If unmuting, set all repo PRs hot and fetch details
	if wasMuted {
		prs := d.store.ListPRsByRepo(muteMsg.Repo)
		for _, pr := range prs {
			d.store.SetPRHot(pr.ID)
			go d.fetchPRDetailsImmediate(pr.ID)
		}
	}
	d.broadcastRepoStates()
	d.broadcastPRs()
```

**Step 5: Run tests**

```bash
go test ./internal/daemon -v
```

**Step 6: Commit**

```bash
git add internal/daemon/daemon.go internal/daemon/websocket.go
git commit -m "feat(daemon): trigger immediate detail fetch on user interactions"
```

---

## Task 8: Update Change Detection for CI Status

**Files:**
- Modify: `internal/store/store.go:298-410` (SetPRs - HasNewChanges computation)

**Step 1: Update HasNewChanges to include CI status for authored/approved PRs**

In `SetPRs`, update the HasNewChanges computation:

```go
// Get interaction data for HasNewChanges computation
interactions := make(map[string]struct {
	lastSeenSHA          string
	lastSeenCommentCount int
	lastSeenCIStatus     string
})
interRows, err := s.db.Query(`SELECT pr_id, last_seen_sha, last_seen_comment_count, last_seen_ci_status FROM pr_interactions`)
if err == nil {
	defer interRows.Close()
	for interRows.Next() {
		var prID string
		var lastSHA, lastCIStatus sql.NullString
		var lastComments sql.NullInt64
		interRows.Scan(&prID, &lastSHA, &lastComments, &lastCIStatus)
		interactions[prID] = struct {
			lastSeenSHA          string
			lastSeenCommentCount int
			lastSeenCIStatus     string
		}{
			lastSeenSHA:          lastSHA.String,
			lastSeenCommentCount: int(lastComments.Int64),
			lastSeenCIStatus:     lastCIStatus.String,
		}
	}
}
```

Update the HasNewChanges computation in the loop:

```go
// Compute HasNewChanges based on interaction tracking
if inter, ok := interactions[pr.ID]; ok {
	// PR has been visited before - check for changes
	if pr.HeadSHA != "" && inter.lastSeenSHA != "" && pr.HeadSHA != inter.lastSeenSHA {
		pr.HasNewChanges = true
	}
	if pr.CommentCount > inter.lastSeenCommentCount {
		pr.HasNewChanges = true
	}
	// CI status changes only matter for authored or approved PRs
	if (pr.Role == protocol.PRRoleAuthor || pr.ApprovedByMe) && pr.CIStatus != "" {
		// CI finished (was pending, now success/failure)
		if inter.lastSeenCIStatus == "pending" && (pr.CIStatus == "success" || pr.CIStatus == "failure") {
			pr.HasNewChanges = true
		}
	}
}
```

**Step 2: Update MarkPRVisited to save CI status**

```go
func (s *Store) MarkPRVisited(prID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	// Get current PR state
	var headSHA, ciStatus sql.NullString
	var commentCount int
	err := s.db.QueryRow("SELECT head_sha, comment_count, ci_status FROM prs WHERE id = ?", prID).Scan(&headSHA, &commentCount, &ciStatus)
	if err != nil {
		return
	}

	// Upsert interaction record
	now := time.Now().Format(time.RFC3339)
	s.db.Exec(`
		INSERT INTO pr_interactions (pr_id, last_visited_at, last_seen_sha, last_seen_comment_count, last_seen_ci_status)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(pr_id) DO UPDATE SET
			last_visited_at = excluded.last_visited_at,
			last_seen_sha = excluded.last_seen_sha,
			last_seen_comment_count = excluded.last_seen_comment_count,
			last_seen_ci_status = excluded.last_seen_ci_status`,
		prID, now, headSHA.String, commentCount, ciStatus.String,
	)
}
```

**Step 3: Run tests**

```bash
go test ./internal/store -v
```

**Step 4: Commit**

```bash
git add internal/store/store.go
git commit -m "feat(store): include CI status in change detection for authored/approved PRs"
```

---

## Task 9: Build and Manual Test

**Step 1: Build and install**

```bash
make install
```

**Step 2: Start the app**

```bash
cd app && pnpm run dev:all
```

**Step 3: Manual verification**

1. Open dashboard, observe PRs loading
2. Click a PR link → should see immediate detail fetch in daemon logs
3. Wait 30s → hot PRs should refresh
4. Wait 3 min → PRs should transition to warm (2 min refresh)
5. Approve a PR → should see immediate fetch, PR stays hot
6. Unmute a PR → should see immediate fetch, PR becomes hot

**Step 4: Check daemon logs**

```bash
tail -f ~/.attn/daemon.log | grep -E "(Detail refresh|Immediate fetch|heat)"
```

Expected output patterns:
- `Detail refresh: N PRs need refresh`
- `Immediate fetch complete for owner/repo#123 (heat=hot)`
- `Detail refresh: updated N PRs`

**Step 5: Commit all if working**

```bash
git add -A
git commit -m "feat: implement PR detail refresh with heat states"
```

---

## Summary

| Task | Description |
|------|-------------|
| 1 | Add heat_state columns to database schema |
| 2 | Add heat state constants and PR fields |
| 3 | Update store to read/write heat state |
| 4 | Add store methods for heat management |
| 5 | Update UpdatePRDetails to store HeadSHA |
| 6 | Add detail refresh to daemon poll cycle |
| 7 | Trigger immediate detail fetch on user interactions |
| 8 | Update change detection for CI status |
| 9 | Build and manual test |
