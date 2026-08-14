// useCommand — a view acting, as a view sees it.
//
// A command is the way out for the way in `useQuery` opened: the view names one
// of the commands its manifest declares, the host addresses it to this app, and
// the app's own handler runs in the sidecar with the same document access it has
// on a bus event. The view is never the authority for the app's rules.
//
// One rule shapes the shape: **the returned runner never rejects.** A view calls
// it straight from an onClick, and a rejected promise nobody awaited is an
// unhandled rejection that reaches attn's console rather than the user's tile.
// It resolves with an outcome to branch on, and mirrors the same failure into
// `error` for the common case of just rendering it.
//
// Design: docs/plans/2026-08-13-ext-a5-ui-host-and-app-sdk.md, "Protocol
// envelopes".

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useAppViewRuntime } from "./runtime"

/** What one invocation did. Never a rejection — see the file header. */
export type CommandOutcome =
  | { ok: true; value: unknown }
  | { ok: false; error: string }

/**
 * A command, ready to invoke. It is a function first, because that is what a
 * view does with it; `pending` and `error` are what it renders around the call.
 */
export interface CommandRunner {
  (payload?: unknown): Promise<CommandOutcome>
  /** True from the call until the daemon answers. */
  readonly pending: boolean
  /**
   * The last failure, cleared by the next call. It is a message meant to be
   * shown: it names the app, the command and the way forward.
   */
  readonly error: string | null
}

/**
 * Invoke one of this app's declared commands.
 *
 * ```tsx
 * const approve = useCommand("approve")
 * <Button variant="primary" disabled={approve.pending} onClick={() => approve({ id })}>
 *   Approve
 * </Button>
 * {approve.error && <p>{approve.error}</p>}
 * ```
 *
 * The command must appear in a `[[commands]]` block of attn-app.toml and the
 * bundle must export a handler under `command:<name>` — the generated `Handlers`
 * type makes the second half a compile error at `attn app apply`.
 */
export function useCommand(command: string): CommandRunner {
  const runtime = useAppViewRuntime()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // A tile can be undocked while a command is in flight. Answering into an
  // unmounted component is a warning nobody can act on, and the daemon has
  // already recorded the invocation either way.
  const live = useRef(true)
  useEffect(() => {
    live.current = true
    return () => {
      live.current = false
    }
  }, [])

  const run = useCallback(
    async (payload?: unknown): Promise<CommandOutcome> => {
      if (!runtime) {
        const message = `useCommand("${command}") was called outside an attn app view host, so there is no daemon to run it.`
        setError(message)
        return { ok: false, error: message }
      }
      setPending(true)
      setError(null)
      try {
        const value = await runtime.command(command, payload)
        return { ok: true, value }
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err)
        if (live.current) setError(message)
        return { ok: false, error: message }
      } finally {
        if (live.current) setPending(false)
      }
    },
    [runtime, command],
  )

  return useMemo(() => {
    const runner = ((payload?: unknown) => run(payload)) as {
      (payload?: unknown): Promise<CommandOutcome>
      pending: boolean
      error: string | null
    }
    runner.pending = pending
    runner.error = error
    return runner as CommandRunner
  }, [run, pending, error])
}
