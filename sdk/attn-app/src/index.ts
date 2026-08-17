// The attn app SDK.
//
// One source, two consumers that cannot disagree within a build:
//
//   - attn's frontend imports this directory through the specifier itself, so a
//     view's React and attn's React are the same module instance.
//   - `attn app apply` materializes its *declarations* into the app being built
//     (see internal/appbuild/sdk.go), so an author typechecks against exactly
//     what will be there at mount time.
//
// Design: docs/plans/2026-08-13-ext-a5-ui-host-and-app-sdk.md.

import type { CurrentStateSnapshot } from "./currentState"

// React, re-exported by name.
//
// An app never writes the specifier `react` — there is no `react` in an app's
// node_modules and nothing declares it, so an import of it fails to typecheck.
// What is re-exported here is what the platform promises; `export *` would make
// React's whole surface the SDK's contract and every React major an SDK contract
// change. The list grows when a real view needs an entry.
export {
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useReducer,
  useRef,
  useState,
} from "react"
export type { ReactElement, ReactNode } from "react"

/** The app contract version this SDK describes — attn-app.toml's attn_app_api. */
export const APP_API_VERSION = 1

/**
 * One fact from attn's durable event bus.
 *
 * A fact says something happened to something; it is an invalidation, not a
 * payload. A handler that needs the current state reads it — the fact may
 * already be behind by the time the handler runs, and the store is what is
 * true now.
 */
export interface AppEvent {
  /** Dotted fact name, for example "session.state.changed". */
  readonly name: string
  /** The entity the fact is about — a session id, a ticket id, an app name. */
  readonly subject: string
  /** Position in the durable log. Opaque and monotonic; do not persist it as a cursor. */
  readonly seq: number
  /**
   * The producer's payload, when it carries one. Typed as unknown by design:
   * the SDK gains a type per fact class as each one's shape is documented,
   * and claiming a shape it has not pinned would be worse than honest unknown.
   */
  readonly payload: unknown
  /** When the fact was published, RFC3339. */
  readonly published_at: string
}

/** One stored document. Body is what was written, byte for byte. */
export interface Document<Body = unknown> {
  readonly id: string
  readonly body: Body
  /**
   * Counts writes to this document. Hand it back to put() to refuse an edit
   * built on a version somebody else has already replaced.
   */
  readonly rev: number
  readonly created_at: string
  readonly updated_at: string
}

/** The comparisons a declared field supports. */
export type FilterOp = "eq" | "lt" | "lte" | "gt" | "gte"

export interface Filter {
  /** A field this collection declared, or `created_at` / `updated_at`. */
  field: string
  op: FilterOp
  value: string | number | boolean
}

export interface QueryOptions {
  filters?: Filter[]
  sort?: { field: string; desc?: boolean }
  /** Defaults to 100, and refuses more than 1000. */
  limit?: number
  /** The id of the previous page's last document. */
  after?: string
}

export interface PutOptions {
  /**
   * The rev the caller read. The write is refused when the stored document
   * has moved on, which is how a read-modify-write stays safe without a lock.
   */
  ifRev?: number
}

/**
 * One of the app's document collections, scoped to its own namespace. An app
 * can only ever address its own documents: the namespace is derived from the
 * app's name, not passed in.
 */
export interface Collection {
  get<Body = unknown>(id: string): Promise<Document<Body> | null>
  put<Body = unknown>(id: string, body: Body, options?: PutOptions): Promise<Document<Body>>
  delete(id: string): Promise<boolean>
  query<Body = unknown>(options?: QueryOptions): Promise<Document<Body>[]>
  count(options?: Pick<QueryOptions, "filters">): Promise<number>
}

/** What a handler is given besides the fact. */
export interface AppContext<Collections> {
  /** This app's name — its registry key, its consumer, its namespace. */
  readonly app: string
  /** The version id that is running, which a rollback does not rewrite. */
  readonly version: number
  /** The collections declared in attn-app.toml, by name. */
  readonly collections: Collections
  /**
   * Read-only current truth, from the same domain projection attn's own UI is
   * handed when it connects. This is how a handler answers "what is true now"
   * without replaying facts it may no longer be able to see; it is the whole
   * read surface a reconcile has besides the app's own collections.
   */
  readonly current: {
    /**
     * One consistent read of attn's state-bearing domains, stamped with the bus
     * position it was taken at. Every mutation at or below `asOfSeq` is already
     * in it; a later one still has a fact coming.
     */
    snapshot(): Promise<CurrentStateSnapshot>
  }
}

/**
 * A handler. Throwing fails that delivery: the bus retries it with backoff
 * rather than skipping it, every attempt is recorded, and an app stuck on one
 * event long enough is disabled so a single broken app cannot hold the event
 * log open for everyone.
 */
export type Handler<Collections> = (
  event: AppEvent,
  ctx: AppContext<Collections>,
) => void | Promise<void>

/**
 * Why attn requires the app to rebuild its derived collections.
 *
 * - `gap` — the consumer resumed below the oldest fact still in the log. Those
 *   facts are gone; no retry brings them back.
 * - `version_changed` — a different version is serving, so what this app
 *   derives from the same facts has changed and its existing documents were
 *   never recomputed. A rollback is a version change too.
 *
 * Re-enabling is neither: an installed app's facts wait while it is disabled,
 * so enabling delivers that backlog in order. Nor is a first install, which has
 * no derived state to rebuild.
 */
export type ReconcileCause = "gap" | "version_changed"

export type {
  AppRegistryEntry,
  AppViewInfo,
  AuthorState,
  CrewMember,
  CurrentStateSnapshot,
  EndpointCapabilities,
  EndpointInfo,
  PR,
  RepoState,
  Seed,
  SeedEdge,
  SeedPlotProgress,
  SeedVar,
  Session,
  TicketRow,
  Workspace,
  WorkspaceLayout,
  WorkspacePane,
} from "./currentState"

/** The durable requests coalesced into one reconcile invocation. */
export interface ReconcileReason {
  /** Sorted as gap, version_changed, independent of arrival order. */
  readonly causes: readonly ReconcileCause[]
  /** The version whose reconcile handler is running. */
  readonly version: number
  /** The bus position the rebuilt state supersedes. */
  readonly throughSeq: number
  readonly gap?: {
    readonly cursor: number
    readonly earliest: number
    readonly missed: number
  }
  /** Distinct prior versions named by pointer moves, in request order. */
  readonly previousVersions: readonly number[]
}

/**
 * Rebuilds the app's collections from current truth, read through
 * `ctx.current.snapshot()` rather than replayed from facts.
 *
 * The contract the runtime relies on:
 *
 * - **It converges.** Running it twice, or after an attempt that died halfway,
 *   ends with the same collection contents. attn will do both — a failed
 *   attempt is retried, and a daemon restart leaves the rebuild still owed.
 * - **It deletes as well as upserts.** A rebuild that only writes leaves rows
 *   current truth no longer has, and nothing else will remove them.
 * - **It yields.** Every app's handlers share one event loop. Awaiting attn's
 *   APIs keeps it turning; a synchronous loop that never yields freezes every
 *   app until attn kills the shared runtime out from under it.
 *
 * While a rebuild is owed or running, this app receives no fact and every
 * command against it is refused with the code `reconcile_owed`. Views stay
 * mounted and observe intermediate writes, so a rebuild that must swap
 * atomically writes a generation marker in its collection and switches it last.
 *
 * A rebuild that keeps throwing is retried with backoff, recorded attempt by
 * attempt, and after fifteen minutes on the same claim the app is disabled with
 * a notification — still owing the rebuild. `attn app status <name>` shows all
 * of it.
 */
export type ReconcileHandler<Collections> = (
  reason: ReconcileReason,
  ctx: AppContext<Collections>,
) => void | Promise<void>

/**
 * What a view is given. A view is a function of where it sits: the first three
 * are ambient, and `params` is what the user typed when docking, which is what
 * makes two tiles of one view show different things.
 */
export interface ViewProps {
  /** The workspace this tile is in. */
  readonly workspaceId: string
  /** The session that workspace has selected, if any. */
  readonly sessionId: string | null
  /** Stable for the life of this docked tile. */
  readonly tileId: string
  /** Opaque to attn — the app decides what it means. Empty when none was given. */
  readonly params: string
}

// Live queries. The runtime seam is exported because the HOST implements it;
// a view only ever calls the hook.
export { useQuery } from "./useQuery"
export type { LiveQueryOptions, QueryError, QueryResult } from "./useQuery"
export { useCommand } from "./useCommand"
export type { CommandOutcome, CommandRunner } from "./useCommand"

// The component slice. Styles come from attn's own build, not from here — see
// the header of components.tsx.
export { Button, EmptyState, List, ListRow, Markdown, TextArea, TextInput } from "./components"
export type {
  ButtonProps,
  ButtonVariant,
  EmptyStateProps,
  ListProps,
  ListRowProps,
  MarkdownProps,
  TextAreaProps,
  TextInputProps,
} from "./components"

export { AppViewRuntimeProvider, useAppViewRuntime } from "./runtime"
export type {
  AppViewRuntime,
  DocumentRevision,
  DocumentSubscriber,
  LiveQueryRequest,
  QueryDelivery,
  RawDocument,
} from "./runtime"
