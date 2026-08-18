// Wire types for the relay socket: the driver-owned unix socket that the
// pi-side suite connects back to. Same ndjson JSON-RPC 2.0 framing as
// attn-rpc.ts, but this is a second, independent connection and id space.

// suite -> driver requests
// `dropped_reports` is how many reports the suite could not hand over since its
// last hello. The suite has no way to log — a stray write corrupts pi's TUI —
// so the count travels here and the driver writes the line.
// `pi_state` is what pi is right now, said on every hello. It is not a
// declaration: attn takes it only when it has nothing (see `only_if_unknown`),
// because applying it on every reconnect would restamp `state_since` and
// re-open a settled turn each time the channel blinks.
export type RelayHelloParams = {
  token: string;
  pi_session_id: string;
  pi_version: string;
  reason: string;
  dropped_reports?: number;
  pi_state?: RelayHelloState;
};
export type RelayHelloState = "idle" | "working" | "pending_approval";
export type RelayHelloResult = { ok: true };
// `pending_approval` is pi blocked on a question only the user can answer —
// today, auto mode's breaker. attn's own validator has always accepted it; the
// suite simply never had a window to report it from.
export type RelaySuiteState = "working" | "pending_approval";
export type RelayReportStateParams = { token: string; state: RelaySuiteState };
// `aborted` is the user having taken the turn back (pi's stopReason). It exists
// so the driver reports `idle` instead of paying a classifier to answer a
// question the session already knows the answer to.
export type RelayReportStopParams = { token: string; assistant_text: string; aborted?: boolean };
// One call auto mode refused, on its way to attn's own surfaces. `rule` names
// who decided (a static envelope rule, `classifier-2a`/`-2b`,
// `classifier-unavailable`, the breaker) and
// `at` is when the session refused it, RFC 3339.
export type RelayReportDenialParams = {
  token: string;
  tool: string;
  action: string;
  reason: string;
  rule: string;
  at: string;
};

// driver -> suite request
export type RelayDeliverMessageParams = { text: string };
export type RelayDeliverMessageResult = { delivered: boolean };

export const relayMethods = {
  hello: "suite.hello",
  reportState: "suite.report_state",
  reportStop: "suite.report_stop",
  reportDenial: "suite.report_denial",
  deliverMessage: "driver.deliver_message",
} as const;
