# Troubleshooting

## Sessions do not appear in the app

- Check the daemon: `attn status` should show a summary rather than "daemon offline".
- Restart the daemon: `pkill -f "attn daemon"` then `attn` to relaunch.

## Resume toggle does nothing

- Verify the agent CLI is installed and on your PATH:
  - `claude --help`
  - `codex --help`
- If you customized the executable path, set it in Settings.

## GitHub PRs are missing

- Ensure `gh` is authenticated: `gh auth status`.
- Check rate limits in Settings.

## Worktree creation fails

- Worktrees require a clean git state in the main repo.
- If a worktree already exists at the target path, remove it first or choose another path.

## Slow startup or missing UI assets

- Run `make install-all` again to rebuild and reinstall the app.
- If you are developing, run `pnpm install` and `pnpm run dev` in `app/`.

## Homebrew upgrade does not update attn

- Refresh indexes: `brew update`.
- Check what is installed:
  - CLI/formula: `brew list --formula | rg '^attn$|victorarias/attn/attn'`
  - App/cask: `brew list --cask | rg '^attn$|victorarias/attn/attn'`
- Upgrade explicitly:
  - formula: `brew upgrade victorarias/attn/attn`
  - cask: `brew upgrade --cask victorarias/attn/attn`

## App says update available but you still run old version

- If installed via cask, run: `brew upgrade --cask victorarias/attn/attn`.
- If installed via DMG/manual, reinstall from:
  - `https://github.com/victorarias/attn/releases/latest`
