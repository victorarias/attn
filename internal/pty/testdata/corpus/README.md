# Ghostty terminal corpus goldens

These raw byte streams were recorded on 2026-07-24 with `script -q` inside macOS tmux panes at the size encoded in each filename. Their Ghostty viewport-text goldens are regression fixtures for attn's single parsed terminal model. They exercise terminal parser behavior observed in real interactive sessions, rather than hand-authored escape sequences.

| Fixture | Origin and coverage |
| --- | --- |
| `claude-approval-80x24.bytes` | A real Claude session, including a Bash approval dialog. Exercises interactive application redraws, cursor motion, and an approval prompt. |
| `codex-approval-80x24.bytes` | A real Codex session, including its approval dialog and a declined network approval. Ghostty's golden render was verified on 2026-07-24 by replaying the fixture in a real 80x24 tmux terminal: the render was identical and the cursor was `(0,17)`. |
| `fish-resize-80x24.bytes` | A fish shell session with a tmux resize from 80x24 to 120x40 and back to 80x24. `script` records no resize event; replay intentionally keeps the fixture at 80x24. |
| `sgr-heavy-80x24.bytes` | zsh colored `git log` output plus an SGR sampler covering 256-color, truecolor, reverse video, and trailing spaces at 80x24. |
| `sgr-heavy-120x40.bytes` | The same zsh colored `git log` and SGR sampler at 120x40. |
| `vim-altscreen-80x24.bytes` | `vim -u NONE` scrolling `AGENTS.md` in the alternate screen at 80x24. |
| `vim-altscreen-120x40.bytes` | The same `vim -u NONE` alternate-screen scroll at 120x40. |

Regenerate the viewport goldens after deliberately changing Ghostty parsing with:

```bash
go test ./internal/pty -run TestGhosttyCorpusGoldens -update
```

To record a fixture, run `script -q` inside a tmux pane first sized to the target grid, then exercise the program and exit `script`. Use `git --no-pager` when capturing Git output. `script` requires a real TTY and hangs without one.
