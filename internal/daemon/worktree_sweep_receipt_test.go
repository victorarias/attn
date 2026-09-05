package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/store"
)

// Applies the shipped gates read-only to real repositories and prints what the
// sweep would do. Run it before changing a gate; docs/worktree-sweep.md says how.
func TestWorktreeSweepReceipt(t *testing.T) {
	repos := strings.Split(strings.TrimSpace(os.Getenv("ATTN_SWEEP_RECEIPT_REPOS")), ",")
	if len(repos) == 0 || repos[0] == "" {
		t.Skip("set ATTN_SWEEP_RECEIPT_REPOS to a comma-separated list of repositories")
	}

	now := time.Now()
	idle := worktreeSweepIdle()
	totals := map[store.WorktreeSweepStatus]int{}

	for _, repo := range repos {
		repo = git.CanonicalizePath(strings.TrimSpace(repo))
		facts := receiptFacts(t, repo, now)
		states, err := git.ListWorktreeStates(repo)
		if err != nil {
			t.Fatalf("%s: listing worktrees: %v", repo, err)
		}

		fmt.Printf("\n== %s   integration branch %s\n", repo, facts.integrationBranch)
		var lines []string
		for _, state := range states {
			if state.Path == repo {
				continue
			}
			observation, _ := observeWorktree(facts, state, now)
			row := &store.Worktree{
				Path: state.Path, Branch: observation.Branch, MainRepo: repo,
				HeadSHA: observation.HeadSHA, Detached: observation.Detached,
				Dirty: observation.Dirty, DirtyFiles: observation.DirtyFiles,
				Stashes: observation.Stashes, Unpushed: observation.Unpushed,
				MergedSignal: observation.MergedSignal, Prunable: observation.Prunable,
				ObservedAt: now.Format(time.RFC3339), RefreshError: observation.Error,
			}
			if !observation.LastActivityAt.IsZero() {
				row.LastActivityAt = observation.LastActivityAt.Format(time.RFC3339)
			}
			verdict := worktreeSweepVerdict(row, sweepContext{
				liveSessions: facts.liveSessions,
				openSeeds:    facts.openSeeds,
				integration:  facts.integrationBranch,
			}, now, idle)
			totals[verdict.Status]++

			idleDays := -1
			if !observation.LastActivityAt.IsZero() {
				idleDays = int(now.Sub(observation.LastActivityAt).Hours() / 24)
			}
			lines = append(lines, fmt.Sprintf("%-12s idle %3dd  %-24s %-28s %s",
				verdict.Status, idleDays, truncate(row.Branch, 24), truncate(verdict.Reason, 28), state.Path))
		}
		sort.Strings(lines)
		for _, line := range lines {
			fmt.Println(line)
		}
	}

	fmt.Println("\n== totals")
	for _, status := range sortedStatuses(totals) {
		fmt.Printf("%-20s %d\n", status, totals[status])
	}
}

func sortedStatuses(totals map[store.WorktreeSweepStatus]int) []store.WorktreeSweepStatus {
	var out []store.WorktreeSweepStatus
	for status := range totals {
		out = append(out, status)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func truncate(value string, width int) string {
	if len(value) <= width {
		return value
	}
	return value[:width-1] + "…"
}

// The merged record comes from `gh`, the query the daemon runs through its GitHub
// client, so the receipt exercises the shipped ladder without a daemon.
func receiptFacts(t *testing.T, repo string, now time.Time) *repositoryFacts {
	t.Helper()

	facts := &repositoryFacts{
		repo:            repo,
		mergedBranches:  map[string]store.MergedBranch{},
		liveSessions:    map[string][]string{},
		openSeeds:       map[string][]string{},
		sessionActivity: map[string]time.Time{},
	}

	gh := exec.Command("gh", "pr", "list", "--state", "merged",
		"--limit", "400", "--json", "number,url,headRefName,headRefOid,baseRefName,mergedAt")
	gh.Dir = repo
	out, err := gh.Output()
	baseCounts := map[string]int{}
	if err != nil {
		t.Logf("%s: gh pr list unavailable (%v); the ladder falls back to local probes", repo, err)
	} else {
		var merged []struct {
			Number      int    `json:"number"`
			URL         string `json:"url"`
			HeadRefName string `json:"headRefName"`
			HeadRefOid  string `json:"headRefOid"`
			BaseRefName string `json:"baseRefName"`
			MergedAt    string `json:"mergedAt"`
		}
		if err := json.Unmarshal(out, &merged); err != nil {
			t.Fatalf("%s: parsing gh output: %v", repo, err)
		}
		for _, pr := range merged {
			facts.mergedBranches[pr.HeadRefName] = store.MergedBranch{
				Branch: pr.HeadRefName, MergedAt: pr.MergedAt, Number: pr.Number,
				URL: pr.URL, HeadSHA: pr.HeadRefOid,
			}
			baseCounts[pr.BaseRefName]++
		}
	}

	base, ok := modalBaseBranch(baseCounts)
	if !ok {
		if fallback, err := git.GetDefaultBranch(repo); err == nil {
			base = fallback
		}
	}
	facts.integrationBranch = resolveIntegrationRef(repo, base)

	if hashes, err := git.TreeHashesOnHistory(repo, facts.integrationBranch); err == nil {
		facts.treeHashes = hashes
	}
	if stashes, err := git.StashCountsByBranch(repo); err == nil {
		facts.stashes = stashes
	}
	_ = now
	return facts
}
