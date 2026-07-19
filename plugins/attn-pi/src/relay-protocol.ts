// Wire types for the relay socket: the driver-owned unix socket that the
// pi-side suite connects back to. Same ndjson JSON-RPC 2.0 framing as
// attn-rpc.ts, but this is a second, independent connection and id space.

// suite -> driver requests
export type RelayHelloParams = { token: string; pi_session_id: string; pi_version: string; reason: string };
export type RelayHelloResult = { ok: true };
export type RelayReportStateParams = { token: string; state: "working" };
export type RelayReportStopParams = { token: string; assistant_text: string };

// driver -> suite request
export type RelayDeliverMessageParams = { text: string };
export type RelayDeliverMessageResult = { delivered: boolean };

export const relayMethods = {
  hello: "suite.hello",
  reportState: "suite.report_state",
  reportStop: "suite.report_stop",
  deliverMessage: "driver.deliver_message",
} as const;
