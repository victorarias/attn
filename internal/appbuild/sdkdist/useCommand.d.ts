/** What one invocation did. Never a rejection — see the file header. */
export type CommandOutcome = {
    ok: true;
    value: unknown;
} | {
    ok: false;
    error: string;
};
/**
 * A command, ready to invoke. It is a function first, because that is what a
 * view does with it; `pending` and `error` are what it renders around the call.
 */
export interface CommandRunner {
    (payload?: unknown): Promise<CommandOutcome>;
    /** True from the call until the daemon answers. */
    readonly pending: boolean;
    /**
     * The last failure, cleared by the next call. It is a message meant to be
     * shown: it names the app, the command and the way forward.
     */
    readonly error: string | null;
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
 * bundle must export a handler under `commands` — the generated `Handlers`
 * type makes the second half a compile error at `attn app apply`.
 */
export declare function useCommand(command: string): CommandRunner;
