// The socket half of the app runtime: newline-delimited JSON-RPC 2.0 over the
// daemon's unix socket, full duplex.
//
// It is the same framing the plugin protocol uses (internal/daemon/plugin_rpc.go)
// and deliberately not a second one — the daemon already has a reader that
// switches to line framing after a hello, and a sidecar that spoke its own
// dialect would be a second wire format to keep correct for no gain.
//
// Both directions carry requests. The daemon asks this process to run a handler;
// this process asks the daemon to read and write documents while that handler
// runs. Ids are per-direction, so the two spaces may collide harmlessly.

import { connect, type Socket } from "node:net"

export interface RpcRequest {
  method: string
  params: unknown
  /** Echo this back verbatim: the daemon matches responses by the id's raw text. */
  id: unknown
}

export interface RpcError {
  code: number
  message: string
}

export const RPC_PARSE_ERROR = -32700
export const RPC_INVALID_REQUEST = -32600
export const RPC_METHOD_NOT_FOUND = -32601
export const RPC_INTERNAL_ERROR = -32603

interface RpcFrame {
  jsonrpc?: string
  id?: unknown
  method?: string
  params?: unknown
  result?: unknown
  error?: RpcError
}

/** Thrown when the daemon answers one of our calls with an error frame. */
export class RpcCallError extends Error {
  readonly code: number
  constructor(code: number, message: string) {
    super(message)
    this.name = "RpcCallError"
    this.code = code
  }
}

type Pending = {
  resolve: (value: unknown) => void
  reject: (reason: Error) => void
}

/**
 * One connection to the daemon.
 *
 * `onRequest` handles inbound calls. It is invoked without awaiting, so a slow
 * handler never blocks the read loop — a dispatch that takes a second must not
 * stall the document reads of a dispatch already in flight.
 */
export class RpcConnection {
  private readonly socket: Socket
  private readonly pending = new Map<string, Pending>()
  private buffer = ""
  private nextId = 0
  private closed: Error | null = null

  constructor(
    socketPath: string,
    private readonly onRequest: (request: RpcRequest) => Promise<unknown>,
  ) {
    this.socket = connect(socketPath)
    this.socket.setNoDelay(true)
    this.socket.on("data", (chunk) => this.consume(chunk))
    this.socket.on("error", (err) => this.fail(err))
    this.socket.on("close", () => this.fail(new Error("the daemon closed the connection")))
  }

  /** Resolves once the socket is connected, or rejects if it never gets there. */
  ready(): Promise<void> {
    return new Promise((resolve, reject) => {
      if (this.closed) {
        reject(this.closed)
        return
      }
      this.socket.once("connect", () => resolve())
      this.socket.once("error", (err) => reject(err))
    })
  }

  /** Resolves when the connection ends, whatever ends it. */
  done(): Promise<Error> {
    return new Promise((resolve) => {
      if (this.closed) {
        resolve(this.closed)
        return
      }
      this.socket.once("close", () =>
        resolve(this.closed ?? new Error("the daemon closed the connection")),
      )
    })
  }

  /** Calls the daemon and resolves with its result. */
  call(method: string, params: unknown): Promise<unknown> {
    if (this.closed) return Promise.reject(this.closed)
    this.nextId += 1
    const id = this.nextId
    const key = String(id)
    return new Promise((resolve, reject) => {
      this.pending.set(key, { resolve, reject })
      try {
        this.write({ jsonrpc: "2.0", id, method, params })
      } catch (err) {
        this.pending.delete(key)
        reject(err instanceof Error ? err : new Error(String(err)))
      }
    })
  }

  /**
   * Tells the daemon something without asking for an answer.
   *
   * A closed socket is not an error here: the daemon is what would have listened,
   * and a notification exists precisely because nothing depends on it arriving.
   */
  notify(method: string, params: unknown): void {
    if (this.closed) return
    this.socket.write(`${JSON.stringify({ jsonrpc: "2.0", method, params })}\n`)
  }

  respond(id: unknown, result: unknown): void {
    this.write({ jsonrpc: "2.0", id, result })
  }

  respondError(id: unknown, code: number, message: string): void {
    this.write({ jsonrpc: "2.0", id, error: { code, message } })
  }

  close(): void {
    this.socket.destroy()
  }

  private write(frame: RpcFrame): void {
    if (this.closed) throw this.closed
    this.socket.write(`${JSON.stringify(frame)}\n`)
  }

  private consume(chunk: Buffer | string): void {
    this.buffer += typeof chunk === "string" ? chunk : chunk.toString("utf8")
    for (;;) {
      const newline = this.buffer.indexOf("\n")
      if (newline < 0) break
      const line = this.buffer.slice(0, newline).trim()
      this.buffer = this.buffer.slice(newline + 1)
      if (line === "") continue
      let frame: RpcFrame
      try {
        frame = JSON.parse(line) as RpcFrame
      } catch {
        // A frame we cannot parse is the daemon's to know about; there is no id
        // to answer, so say it once on stderr and keep reading.
        process.stderr.write(`app runtime: unparseable frame from the daemon: ${line}\n`)
        continue
      }
      this.route(frame)
    }
  }

  private route(frame: RpcFrame): void {
    // A frame with a method is the daemon calling us; one without is an answer to
    // a call we made. Same rule the daemon applies in the other direction.
    if (frame.method) {
      void this.serve(frame)
      return
    }
    const key = frame.id === undefined ? "" : String(frame.id)
    const pending = this.pending.get(key)
    if (!pending) return
    this.pending.delete(key)
    if (frame.error) {
      pending.reject(new RpcCallError(frame.error.code, frame.error.message))
      return
    }
    pending.resolve(frame.result)
  }

  private async serve(frame: RpcFrame): Promise<void> {
    const id = frame.id
    if (id === undefined || id === null) {
      // Every inbound call must be answerable. A notification would be a request
      // whose failure nobody learns about.
      process.stderr.write(`app runtime: ignoring ${frame.method} with no id\n`)
      return
    }
    try {
      const result = await this.onRequest({
        method: frame.method ?? "",
        params: frame.params,
        id,
      })
      this.respond(id, result)
    } catch (err) {
      this.respondError(id, RPC_INTERNAL_ERROR, describe(err))
    }
  }

  private fail(err: Error): void {
    if (this.closed) return
    this.closed = err
    for (const [, pending] of this.pending) pending.reject(err)
    this.pending.clear()
  }
}

/** Renders anything thrown as a message, keeping a stack when there is one. */
export function describe(err: unknown): string {
  if (err instanceof Error) {
    return err.stack?.trim() || `${err.name}: ${err.message}`
  }
  return String(err)
}
