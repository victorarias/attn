// attn's shared app runtime.
//
// One supervised Bun process runs the handlers of every installed app. It is not
// a sandbox and does not pretend to be one: isolation between apps is failure
// attribution, not an OS boundary. What it owes the daemon is an honest answer
// per dispatch — this app's handler threw, or it did not — and, when an error
// escapes every handler and takes the process down with it, the name of the app
// whose code it came from. Never whichever app happened to be running: see
// installCrashReporter.
//
// It ships as a `bun build --compile` standalone binary. The Bun runtime is inside
// the executable, so a daemon launched by the macOS app needs no PATH resolution
// and a user's machine needs no toolchain to run apps. There is deliberately no
// PATH-resolution fallback: one mechanism, one failure class.
//
// See docs/plans/2026-08-06-ext-a4-app-registry-and-runtime.md.

import { AsyncLocalStorage } from "node:async_hooks"
import { RpcConnection, RPC_METHOD_NOT_FOUND, describe, type RpcRequest } from "./rpc.ts"
import {
  appForStack,
  runCommand,
  runDispatch,
  runReconcile,
  type CommandParams,
  type DispatchParams,
  type ReconcileParams,
} from "./dispatch.ts"

/**
 * The runtime contract this host speaks. The daemon refuses a host that does not
 * match, because a version skew between the daemon and a binary inside an old app
 * bundle is exactly the case a silent mismatch would turn into wrong behavior.
 *
 * Bump it together with appRuntimeAPIVersion in internal/daemon/app_runtime.go.
 */
const APP_RUNTIME_API_VERSION = 5

/**
 * Which app a line of output came from.
 *
 * Handler code writes to the same stdout as everything else in this process, and
 * a shared log with no attribution cannot answer `attn app logs <name>`. The
 * storage is async-scoped, so a log written after an `await` inside a handler is
 * still tagged with the app that wrote it.
 */
const currentApp = new AsyncLocalStorage<string>()

/** The tag `attn app logs <name>` filters on. Kept in step with appRuntimeLogTag in Go. */
function tag(app: string | undefined): string {
  return app ? `[app ${app}] ` : "[runtime] "
}

/**
 * Routes everything this process prints through the app tag.
 *
 * It patches console rather than asking apps to use a logger: an app author
 * writes console.log, and output that vanishes because it did not go through the
 * approved channel is worse than no capture at all.
 */
function captureConsole(): void {
  const write = (stream: NodeJS.WriteStream, args: unknown[]) => {
    const prefix = tag(currentApp.getStore())
    const text = args.map(render).join(" ")
    for (const line of text.split("\n")) stream.write(`${prefix}${line}\n`)
  }
  console.log = (...args: unknown[]) => write(process.stdout, args)
  console.info = (...args: unknown[]) => write(process.stdout, args)
  console.debug = (...args: unknown[]) => write(process.stdout, args)
  console.warn = (...args: unknown[]) => write(process.stderr, args)
  console.error = (...args: unknown[]) => write(process.stderr, args)
}

function render(value: unknown): string {
  if (typeof value === "string") return value
  if (value instanceof Error) return value.stack?.trim() || `${value.name}: ${value.message}`
  try {
    return JSON.stringify(value) ?? String(value)
  } catch {
    return String(value)
  }
}

/**
 * How long the daemon gets to acknowledge a crash report before the process
 * exits anyway.
 *
 * A tripwire past a localhost round trip on a socket that is already open and
 * whose peer answers this method with a constant. The same shape measured on a
 * live daemon — its liveness ping — costs 344–416µs, so a second is ~2,500× the
 * real cost and only a daemon that is itself in trouble reaches it. The wait is
 * bounded because the exit must not be conditional on the daemon: an unreported
 * crash costs the culprit a strike, a hung exit costs every app the runtime.
 */
const CRASH_REPORT_WAIT_MS = 1000

/**
 * Reports the app whose code took the process down, then exits.
 *
 * A rejection nobody handled kills the sidecar for every app, so the app that
 * caused it is precisely the one the auto-disable rule exists to stop — and
 * until this existed it was the one thing structurally exempt from it, because a
 * dead process was charged to nobody.
 *
 * The stack is the only honest witness. Bun offers no other: async_hooks
 * createHook fires no callbacks at all here, and AsyncLocalStorage.getStore() is
 * undefined inside these handlers, both measured. It is also the *right* witness
 * — a floating promise rejects long after the dispatch that started it returned,
 * so "which app is running" routinely names an innocent, while the bundle path
 * in the stack names the author.
 *
 * Nothing before the daemon connection is covered, and nothing needs to be: no
 * app code has loaded yet, so a crash there is a startup failure the supervisor
 * already owns.
 */
function installCrashReporter(connection: RpcConnection): void {
  let crashing = false
  const crash = (kind: string, reason: unknown): void => {
    // A second crash while reporting the first must not restart the wait.
    if (crashing) return
    crashing = true

    const error = render(reason)
    const app = appForStack(error)
    process.stderr.write(
      `${tag(app || undefined)}unhandled ${kind}${app ? ` in app ${app}` : ""}, stopping the app runtime: ${error}\n`,
    )

    const reported = connection.call("app_runtime.crashed", { app, kind, error }).catch(() => {})
    const bounded = new Promise((resolve) => setTimeout(resolve, CRASH_REPORT_WAIT_MS))
    void Promise.race([reported, bounded]).then(() => process.exit(1))
  }

  process.on("unhandledRejection", (reason) => crash("unhandledRejection", reason))
  process.on("uncaughtException", (err) => crash("uncaughtException", err))
}

/** Says why the process cannot start, then stops. The supervisor restarts it. */
function fatal(message: string): never {
  process.stderr.write(`${tag(undefined)}${message}\n`)
  process.exit(1)
}

function requiredEnv(name: string): string {
  const value = process.env[name]?.trim()
  if (!value) {
    fatal(
      `${name} is not set. The app runtime is launched by the attn daemon, which injects it; running this binary by hand is not a supported way to start it.`,
    )
  }
  return value
}

async function main(): Promise<void> {
  captureConsole()

  const socketPath = requiredEnv("ATTN_SOCKET_PATH")
  const generation = Number(requiredEnv("ATTN_APP_RUNTIME_GENERATION"))
  if (!Number.isSafeInteger(generation) || generation <= 0) {
    fatal(
      `ATTN_APP_RUNTIME_GENERATION is ${process.env.ATTN_APP_RUNTIME_GENERATION}, which is not a positive integer. The supervisor fences stale processes by generation, so a host that cannot present its own is refused.`,
    )
  }

  // Declared before the connection so the read loop can reach it: a dispatch can
  // arrive on the same tick the hello result does.
  let connection: RpcConnection
  const serve = async (request: RpcRequest): Promise<unknown> => {
    switch (request.method) {
      case "app.dispatch": {
        const params = request.params as DispatchParams
        // The tag follows the handler through every await it makes.
        return currentApp.run(params.app, () => runDispatch(connection, params))
      }
      case "app.command": {
        const params = request.params as CommandParams
        return currentApp.run(params.app, () => runCommand(connection, params))
      }
      case "app.reconcile": {
        const params = request.params as ReconcileParams
        return currentApp.run(params.app, () => runReconcile(connection, params))
      }
      case "app.runtime.ping":
        // A liveness answer the daemon can ask for without running app code.
        return { ok: true, api_version: APP_RUNTIME_API_VERSION }
      default:
        throw Object.assign(new Error(`unknown method ${request.method}`), {
          code: RPC_METHOD_NOT_FOUND,
        })
    }
  }

  connection = new RpcConnection(socketPath, serve)
  try {
    await connection.ready()
  } catch (err) {
    fatal(`cannot reach the attn daemon at ${socketPath}: ${describe(err)}`)
  }

  try {
    await connection.call("app_runtime.hello", {
      generation,
      api_version: APP_RUNTIME_API_VERSION,
      pid: process.pid,
    })
  } catch (err) {
    fatal(`the daemon refused this app runtime: ${describe(err)}`)
  }

  installCrashReporter(connection)

  console.log(`app runtime ready (generation ${generation}, pid ${process.pid})`)

  // The process lives as long as its connection. A dropped socket is the
  // supervisor's business, not something to paper over with a reconnect loop
  // here: the supervisor already owns backoff, generation fencing and parking,
  // and a second reconnect policy underneath it would fight the first.
  const ended = await connection.done()
  process.stderr.write(`${tag(undefined)}connection to the daemon ended: ${ended.message}\n`)
  process.exit(1)
}

void main()
