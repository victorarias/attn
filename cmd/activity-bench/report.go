package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func runReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	dir := fs.String("dir", defaultDir, "corpus directory")
	file := fs.String("file", "", "results file (default: newest in --dir)")
	lines := fs.Bool("lines", false, "print every generated line, grouped by corpus entry")
	failures := fs.Bool("failures", false, "print only lines that failed a check")
	if err := fs.Parse(args); err != nil {
		return err
	}

	path := *file
	if path == "" {
		var err error
		if path, err = newestResults(*dir); err != nil {
			return err
		}
	}
	results, err := readResults(path)
	if err != nil {
		return err
	}
	fmt.Printf("%s — %d runs\n\n", path, len(results))
	summarize(results)
	if *lines || *failures {
		fmt.Println()
		printLines(results, *failures)
	}
	return nil
}

// summarize is the comparison table: one row per variant. Pass rate is the
// deterministic checks only — it says a variant did not regress a known failure
// mode, not that its lines are good. Reading the lines is still the job.
func summarize(results []Result) {
	type stat struct {
		runs, passed, errored int
		latencies             []int64
		cost                  float64
		promptChars           int
		byCheck               map[string]int
	}
	stats := map[string]*stat{}
	var order []string
	for _, result := range results {
		key := result.Variant.String()
		s, ok := stats[key]
		if !ok {
			s = &stat{byCheck: map[string]int{}}
			stats[key] = s
			order = append(order, key)
		}
		s.runs++
		s.cost += result.CostUSD
		s.promptChars += result.PromptChar
		s.latencies = append(s.latencies, result.LatencyMS)
		if result.Error != "" {
			s.errored++
			continue
		}
		if len(result.Violations) == 0 {
			s.passed++
		}
		for _, violation := range result.Violations {
			s.byCheck[violation.Check]++
		}
	}
	sort.Strings(order)

	fmt.Printf("%-46s %5s %6s %7s %7s %9s %8s\n", "variant", "runs", "pass", "p50 ms", "p95 ms", "$/run", "prompt")
	for _, key := range order {
		s := stats[key]
		sort.Slice(s.latencies, func(i, j int) bool { return s.latencies[i] < s.latencies[j] })
		p50 := percentile(s.latencies, 0.50)
		p95 := percentile(s.latencies, 0.95)
		rate := 0
		if s.runs > 0 {
			rate = 100 * s.passed / s.runs
		}
		fmt.Printf("%-46s %5d %5d%% %7d %7d %9.5f %8d\n",
			truncate(key, 46), s.runs, rate, p50, p95, s.cost/float64(max(s.runs, 1)), s.promptChars/max(s.runs, 1))
	}

	// Failure breakdown last, because which check failed is what tells you what
	// to change in the prompt.
	fmt.Println("\nfailures by check:")
	any := false
	for _, key := range order {
		s := stats[key]
		if len(s.byCheck) == 0 && s.errored == 0 {
			continue
		}
		any = true
		var parts []string
		checks := make([]string, 0, len(s.byCheck))
		for check := range s.byCheck {
			checks = append(checks, check)
		}
		sort.Strings(checks)
		for _, check := range checks {
			parts = append(parts, fmt.Sprintf("%s=%d", check, s.byCheck[check]))
		}
		if s.errored > 0 {
			parts = append(parts, fmt.Sprintf("run_error=%d", s.errored))
		}
		fmt.Printf("  %-46s %s\n", truncate(key, 46), strings.Join(parts, " "))
	}
	if !any {
		fmt.Println("  (none)")
	}
}

// printLines groups by corpus entry so variants can be compared on the same
// input, side by side. This is the part that cannot be automated away.
func printLines(results []Result, onlyFailures bool) {
	byEntry := map[string][]Result{}
	var order []string
	for _, result := range results {
		if _, ok := byEntry[result.EntryID]; !ok {
			order = append(order, result.EntryID)
		}
		byEntry[result.EntryID] = append(byEntry[result.EntryID], result)
	}
	for _, entryID := range order {
		group := byEntry[entryID]
		var shown []Result
		for _, result := range group {
			if onlyFailures && len(result.Violations) == 0 && result.Error == "" {
				continue
			}
			shown = append(shown, result)
		}
		if len(shown) == 0 {
			continue
		}
		fmt.Printf("\n── %s [%s]\n", truncate(entryID, 12), shown[0].State)
		for _, result := range shown {
			mark := "  "
			if result.Error != "" {
				mark = "!!"
			} else if len(result.Violations) > 0 {
				mark = "✗ "
			}
			text := result.Line
			if result.Error != "" {
				text = "ERROR: " + result.Error
			}
			fmt.Printf("  %s %-40s %s\n", mark, truncate(result.Variant.String(), 40), text)
			for _, violation := range result.Violations {
				fmt.Printf("       ↳ %s: %s\n", violation.Check, violation.Message)
			}
		}
	}
}

func newestResults(dir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "results-*.jsonl"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no results in %s — run `activity-bench run` first", dir)
	}
	sort.Strings(matches)
	return matches[len(matches)-1], nil
}

func readResults(path string) ([]Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var results []Result
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var result Result
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func percentile(sorted []int64, f float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted)) * f)
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}
