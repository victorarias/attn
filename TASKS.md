# TASKS

## Review Panel Performance

### Agreed Next (P0)
- [x] Decouple Review Panel open from remote fetch (`git fetch --all --prune`)
  - Open panel immediately using current local refs.
  - Show a non-blocking sync indicator in header: `Syncing with origin...`.
  - Refresh file list when fetch completes.
  - If fetch fails, keep local view and show: `Could not refresh remotes; showing local refs`.
  - Preserve selected file on refresh when possible.
- [ ] Virtualize the review file list
  - Render only visible rows for large file sets.
  - Keep keyboard navigation (`j/k`, `n/p`, `]`) working with virtualization.
- [ ] Cache branch diff file results
  - Cache key: `repo + baseRef + HEAD + dirty-state`.
  - Reuse cached results on reopen.
  - Invalidate on git-status updates, branch switch, repo switch, or explicit refresh.

### Next (P1)
- [ ] Cache per-file diff payloads during a review session
  - Cache key: `repo + baseRef + path + content-hash`.
  - Avoid refetching/rebuilding diff when revisiting a file unchanged.
- [ ] Memoize reviewer output file-link parsing
  - Precompute file lookup map/set once per file list update.
  - Avoid rebuilding markdown reference handlers per event render.
- [ ] Add backend fast mode for branch diff list
  - Return path/status first.
  - Compute additions/deletions lazily or only for visible/selected files.

### Later (P2)
- [ ] Move hunk computation to daemon
  - Send patch/hunks instead of full original+modified file payloads where possible.
- [ ] Add SQLite indexes for review comments
  - Add composite index: `(review_id, filepath, line_start)`.
  - Verify query plans for `get_comments` and `get_comments_for_file`.
- [ ] Add performance instrumentation
  - Track open-to-first-file time, file-switch latency, and render time by repo size.
  - Log percentile timings (p50/p95) for before/after comparison.
