package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

const worktreeUsage = `usage: attn worktree list [--repo <path>] [--limit <n>] [--json]
       attn worktree keep <path>
       attn worktree unkeep <path>
       attn worktree delete <path> [--force]
       attn worktree log [--repo <path>] [--limit <n>] [--json]
       attn worktree refresh

  list      every worktree of every tracked repository, with why the sweep kept it
  keep      pin a worktree so the sweep never reclaims it
  unkeep    drop the pin; the next sweep decides again
  delete    remove a worktree now; --force removes a dirty one
  log       what the sweep removed, newest first
  refresh   ask the daemon to refresh worktree state now instead of on its tick
`

// The default page: fits an 80x24 terminal with the header and the omitted notice.
const worktreeListDefaultLimit = 20

func runWorktree() {
	args := os.Args[2:]
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, worktreeUsage)
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		worktreeList(args[1:])
	case "keep":
		worktreeKeep(args[1:], true)
	case "unkeep":
		worktreeKeep(args[1:], false)
	case "delete":
		worktreeDelete(args[1:])
	case "log":
		worktreeLog(args[1:])
	case "refresh":
		worktreeRefresh()
	case "-h", "--help", "help":
		fmt.Print(worktreeUsage)
	default:
		fmt.Fprintf(os.Stderr, "unknown worktree command: %s\n\n", args[0])
		fmt.Fprint(os.Stderr, worktreeUsage)
		os.Exit(1)
	}
}

func worktreeFlags(name string, args []string) (repo *string, limit *int, asJSON *bool) {
	fs := flag.NewFlagSet("worktree "+name, flag.ExitOnError)
	repo = fs.String("repo", "", "only this repository")
	limit = fs.Int("limit", worktreeListDefaultLimit, "how many rows to show")
	asJSON = fs.Bool("json", false, "print the raw result")
	_ = fs.Parse(args)
	return repo, limit, asJSON
}

func worktreeList(args []string) {
	repo, limit, asJSON := worktreeFlags("list", args)
	warnIfDaemonVersionMismatch()

	result, err := client.New("").WorktreeList(expandWorktreeRepo(*repo), *limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if *asJSON {
		printWorktreeJSON(result)
		return
	}
	if len(result.Worktrees) == 0 {
		fmt.Println("no worktrees tracked yet; run `attn worktree refresh` if a repository is missing")
		return
	}

	integration := make(map[string]string, len(result.Repositories))
	for _, repository := range result.Repositories {
		if branch := protocol.Deref(repository.IntegrationBranch); branch != "" {
			integration[repository.MainRepo] = branch + " (" + protocol.Deref(repository.IntegrationSource) + ")"
		}
	}

	byRepo := map[string][]protocol.Worktree{}
	var order []string
	for _, wt := range result.Worktrees {
		if _, seen := byRepo[wt.MainRepo]; !seen {
			order = append(order, wt.MainRepo)
		}
		byRepo[wt.MainRepo] = append(byRepo[wt.MainRepo], wt)
	}
	sort.Strings(order)

	for i, repoPath := range order {
		if i > 0 {
			fmt.Println()
		}
		header := repoPath
		if branch := integration[repoPath]; branch != "" {
			header += "   merges into " + branch
		}
		fmt.Println(header)
		rows := [][]string{{"WORKTREE", "BRANCH", "STATE", "SWEEP", "WHY"}}
		for _, wt := range byRepo[repoPath] {
			rows = append(rows, []string{
				filepath.Base(wt.Path),
				worktreeBranchLabel(wt),
				worktreeStateLabel(wt),
				worktreeSweepLabel(wt),
				protocol.Deref(wt.SweepReason),
			})
		}
		printWorktreeTable(rows)
	}

	if result.Omitted > 0 {
		fmt.Printf("\nshowing %d, %d omitted, raise the page with --limit %d\n",
			len(result.Worktrees), result.Omitted, len(result.Worktrees)+result.Omitted)
	}
}

func worktreeBranchLabel(wt protocol.Worktree) string {
	if protocol.Deref(wt.Detached) {
		sha := protocol.Deref(wt.HeadSHA)
		if len(sha) > 8 {
			sha = sha[:8]
		}
		return "detached " + sha
	}
	if wt.Branch == "" {
		return "-"
	}
	return wt.Branch
}

func worktreeStateLabel(wt protocol.Worktree) string {
	var parts []string
	if protocol.Deref(wt.Prunable) {
		parts = append(parts, "stale")
	}
	if protocol.Deref(wt.Dirty) {
		parts = append(parts, fmt.Sprintf("dirty %d", protocol.Deref(wt.DirtyFiles)))
	}
	if n := protocol.Deref(wt.Stashes); n > 0 {
		parts = append(parts, fmt.Sprintf("%d stashed", n))
	}
	if n := protocol.Deref(wt.Unpushed); n > 0 {
		parts = append(parts, fmt.Sprintf("%d ahead", n))
	}
	if signal := protocol.Deref(wt.MergedSignal); signal != "" {
		parts = append(parts, "merged/"+signal)
	}
	if protocol.Deref(wt.ObservedAt) == "" {
		parts = append(parts, "not refreshed")
	}
	if len(parts) == 0 {
		return "clean"
	}
	return strings.Join(parts, ", ")
}

func worktreeSweepLabel(wt protocol.Worktree) string {
	status := protocol.Deref(wt.SweepStatus)
	if status == "" {
		return "-"
	}
	if status != "scheduled" {
		return status
	}
	at, err := time.Parse(time.RFC3339, protocol.Deref(wt.SweepAt))
	if err != nil {
		return status
	}
	return "scheduled " + at.Format("2006-01-02")
}

func worktreeKeep(args []string, keep bool) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		verb := "keep"
		if !keep {
			verb = "unkeep"
		}
		fmt.Fprintf(os.Stderr, "usage: attn worktree %s <path>\n", verb)
		os.Exit(1)
	}
	warnIfDaemonVersionMismatch()

	result, err := client.New("").WorktreeKeep(worktreePathArg(args[0]), keep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if keep {
		fmt.Printf("pinned %s; the sweep will never reclaim it\n", result.Worktree.Path)
		return
	}
	fmt.Printf("unpinned %s; the next sweep decides again\n", result.Worktree.Path)
}

func worktreeDelete(args []string) {
	fs := flag.NewFlagSet("worktree delete", flag.ExitOnError)
	force := fs.Bool("force", false, "delete even when the tree has uncommitted or untracked files")
	_ = fs.Parse(args)
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: attn worktree delete <path> [--force]")
		os.Exit(1)
	}
	warnIfDaemonVersionMismatch()

	path := worktreePathArg(fs.Arg(0))
	if err := client.New("").DeleteWorktree(path, *force); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("deleted %s\n", path)
}

func worktreeLog(args []string) {
	repo, limit, asJSON := worktreeFlags("log", args)
	warnIfDaemonVersionMismatch()

	result, err := client.New("").WorktreeSweepLog(expandWorktreeRepo(*repo), *limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if *asJSON {
		printWorktreeJSON(result)
		return
	}
	if len(result.Entries) == 0 {
		fmt.Println("the sweep has not removed anything yet")
		return
	}
	rows := [][]string{{"WHEN", "WORKTREE", "BRANCH", "ACTION", "WHY"}}
	for _, entry := range result.Entries {
		when := entry.At
		if at, err := time.Parse(time.RFC3339Nano, entry.At); err == nil {
			when = at.Format("2006-01-02 15:04")
		}
		rows = append(rows, []string{
			when, entry.Path, protocol.Deref(entry.Branch), entry.Action, protocol.Deref(entry.Reason),
		})
	}
	printWorktreeTable(rows)
	if result.Omitted > 0 {
		fmt.Printf("\nshowing %d, %d omitted, raise the page with --limit %d\n",
			len(result.Entries), result.Omitted, len(result.Entries)+result.Omitted)
	}
}

func worktreeRefresh() {
	warnIfDaemonVersionMismatch()
	result, err := client.New("").WorktreeRefresh()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if !result.Queued {
		fmt.Fprintln(os.Stderr, "the daemon has no job queue running, so no refresh was queued")
		os.Exit(1)
	}
	fmt.Println("refreshing worktree state in the background; `attn worktree list` shows the result")
}

func worktreePathArg(value string) string {
	value = strings.TrimSpace(value)
	if abs, err := filepath.Abs(value); err == nil {
		return abs
	}
	return value
}

func expandWorktreeRepo(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return worktreePathArg(value)
}

func printWorktreeJSON(value any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding result: %v\n", err)
		os.Exit(1)
	}
}

func printWorktreeTable(rows [][]string) {
	if len(rows) == 0 {
		return
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	for _, row := range rows {
		var line strings.Builder
		for i, cell := range row {
			if i == len(row)-1 {
				line.WriteString(cell)
				break
			}
			line.WriteString(cell)
			line.WriteString(strings.Repeat(" ", widths[i]-len(cell)+2))
		}
		fmt.Println(strings.TrimRight(line.String(), " "))
	}
}
