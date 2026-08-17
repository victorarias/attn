// Wire types for the relay socket: the driver-owned unix socket that the
// pi-side suite connects back to. Same ndjson JSON-RPC 2.0 framing as
// attn-rpc.ts, but this is a second, independent connection and id space.

// suite -> driver requests
export type RelayHelloParams = { token: string; pi_session_id: string; pi_version: string; reason: string };
export type RelayHelloResult = { ok: true };
export type RelayReportStateParams = { token: string; state: "working" };
export type RelayReportStopParams = { token: string; assistant_text: string };
// One call auto mode refused, on its way to attn's own surfaces. `rule` names
// who decided (a static envelope rule, `classifier-2a`/`-2b`, the breaker) and
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
