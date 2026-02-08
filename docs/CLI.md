# CLI Reference

The CLI is a thin wrapper around the app and agent CLIs.

## Commands

```
attn
```
Opens the app (or runs the wrapper when launched inside the app).

```
attn -s <label>
```
Starts a session with an explicit label (inside app wrapper).

```
attn --resume
```
Opens the agent's resume picker (inside app wrapper).

```
attn list
```
Outputs all sessions as JSON.

## Examples

```
attn -s payments
attn --resume
```

## Notes

- The CLI is primarily meant to be used from the app; direct use is supported but best paired with the daemon.
- Forking (`--fork-session`) is only supported for Claude.
