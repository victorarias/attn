// useQuery — a live document query, as a view sees it.
//
// The whole A3.4 delivery contract lives here so a view never meets it: render
// `order`, take each body from `upsert` or from the cache, forget everything
// else. What a view gets is an array that stays current.
//
// Design: docs/plans/2026-08-05-ext-a3.4-doc-store-positions-and-windows.md
// (delivery semantics) and docs/plans/2026-08-13-ext-a5-ui-host-and-app-sdk.md
// ("What an author writes").

import { useEffect, useMemo, useState } from "react"
import type { Document, Filter } from "./index"
import {
  useAppViewRuntime,
  type DocumentRevision,
  type QueryDelivery,
  type RawDocument,
} from "./runtime"

/** What a live query takes. No `after`: a live query is a window, a cursor is a walk. */
export interface LiveQueryOptions {
  filters?: Filter[]
  sort?: { field: string; desc?: boolean }
  /** Defaults to the store's own 100, and refuses more than 1000. */
  limit?: number
}

export interface QueryError {
  /**
   * What to act on. `collection_undefined` and `collection_redeclared` mean this
   * query is over; `invalid_query`, `undeclared_collection` and
   * `subscription_limit` mean it never started.
   */
  code: string
  message: string
}

export interface QueryResult<Body> {
  /** The window, in the server's order. */
  docs: Array<Document<Body>>
  /** The log position this window was true at. Opaque and monotonic. */
  asOfSeq: number
  /** Whether the daemon is serving this query right now. */
  live: boolean
  /**
   * Set when the subscription ended and will not resume on its own. It is a
   * state to render, not an exception: a tile that spins forever because its
   * collection was removed is worse than one that says so.
   */
  error: QueryError | null
}

/**
 * How many unmounted queries keep their bodies for a resume.
 *
 * Same receipt as the daemon's per-client subscription tripwire (measured
 * 2026-08-13 against Victor's production database: seven live workspaces, three
 * of them holding one docked tile): a client cannot hold more live queries than
 * that, so retaining more caches than that is retaining for tiles that cannot
 * all exist. Evicting costs one full first delivery instead of a diffed one —
 * bytes, never correctness — so this bound is silent by design.
 */
const RESUME_CACHE_LIMIT = 64

/**
 * Bodies by query, kept past unmount so a remount resumes with `have` and the
 * daemon sends only what changed. Insertion-ordered, refreshed on write, so the
 * oldest untouched query is the one evicted.
 */
const resumeCaches = new Map<string, Map<string, RawDocument>>()

function cacheFor(key: string): Map<string, RawDocument> {
  const existing = resumeCaches.get(key)
  if (existing) {
    resumeCaches.delete(key)
    resumeCaches.set(key, existing)
    return existing
  }
  const fresh = new Map<string, RawDocument>()
  resumeCaches.set(key, fresh)
  while (resumeCaches.size > RESUME_CACHE_LIMIT) {
    const oldest = resumeCaches.keys().next()
    if (oldest.done) break
    resumeCaches.delete(oldest.value)
  }
  return fresh
}

/** The identity of a query, which is what a resume cache is keyed by. */
function queryKey(namespace: string, collection: string, options: LiveQueryOptions): string {
  return JSON.stringify([
    namespace,
    collection,
    (options.filters ?? []).map((f) => [f.field, f.op, f.value]),
    options.sort ? [options.sort.field, !!options.sort.desc] : null,
    options.limit ?? null,
  ])
}

function parseBody<Body>(raw: RawDocument): Document<Body> {
  return {
    id: raw.id,
    body: JSON.parse(raw.body) as Body,
    rev: raw.rev,
    created_at: raw.created_at,
    updated_at: raw.updated_at,
  }
}

/**
 * Subscribe to a collection and stay current.
 *
 * ```tsx
 * const { docs, live } = useQuery("requests", {
 *   filters: [{ field: "status", op: "eq", value: "pending" }],
 *   sort: { field: "updated_at", desc: true },
 *   limit: 20,
 * })
 * ```
 *
 * The collection is this app's — the namespace comes from where the tile is
 * mounted, so a view cannot read another app's documents by asking.
 */
export function useQuery<Body = unknown>(
  collection: string,
  options: LiveQueryOptions = {},
): QueryResult<Body> {
  const runtime = useAppViewRuntime()
  const namespace = runtime?.namespace ?? ""
  const key = queryKey(namespace, collection, options)

  const [state, setState] = useState<{ docs: Array<Document<Body>>; asOfSeq: number }>({
    docs: [],
    asOfSeq: 0,
  })
  const [live, setLive] = useState(false)
  const [error, setError] = useState<QueryError | null>(null)
  // Bumped when a delivery names a body nobody holds. That is the one invariant
  // violation the contract anticipates, and its remedy is to start over with no
  // `have` — invisible to the view beyond one fuller delivery.
  const [generation, setGeneration] = useState(0)

  useEffect(() => {
    if (!runtime) {
      setError({
        code: "no_runtime",
        message:
          "useQuery was called outside an attn app view host, so there is no daemon to query.",
      })
      return
    }
    const after = (options as { after?: unknown }).after
    if (after !== undefined) {
      setError({
        code: "invalid_query",
        message:
          `useQuery cannot take an after cursor (${String(after)}): a live query is a window and a cursor is a walk, ` +
          "so the document it names moves out from under the subscription. Set a limit and render each window.",
      })
      return
    }

    setError(null)
    // Survives this effect, and this mount: it is what `have()` reads on a
    // resubscribe, and what a remount resumes from.
    const cache = cacheFor(key)
    let dropped = false

    const apply = (delivery: QueryDelivery) => {
      for (const doc of delivery.upsert) cache.set(doc.id, doc)
      const docs: Array<Document<Body>> = []
      for (const id of delivery.order) {
        const raw = cache.get(id)
        if (!raw) {
          // A body we neither hold nor were sent. The subscription is broken;
          // start over with an empty cache so the next first delivery is whole.
          cache.clear()
          if (!dropped) setGeneration((n) => n + 1)
          return
        }
        docs.push(parseBody<Body>(raw))
      }
      // The forget rule: anything not named in `order` is gone from the window,
      // so holding its body would resume a query that no longer wants it.
      const named = new Set(delivery.order)
      for (const id of Array.from(cache.keys())) {
        if (!named.has(id)) cache.delete(id)
      }
      setState({ docs, asOfSeq: delivery.asOfSeq })
    }

    const unsubscribe = runtime.subscribe({
      request: {
        namespace: runtime.namespace,
        collection,
        filters: options.filters?.map((f) => ({ field: f.field, op: f.op, value: f.value })),
        sort: options.sort,
        limit: options.limit,
      },
      have: (): DocumentRevision[] =>
        Array.from(cache.values()).map((doc) => ({ id: doc.id, rev: doc.rev })),
      onDelivery: apply,
      onEnded: (code, message) => {
        setLive(false)
        setError({ code, message })
      },
      onLive: setLive,
    })

    return () => {
      dropped = true
      unsubscribe()
    }
    // `key` is the query's whole identity, so it stands in for the options object
    // a caller rebuilds on every render.
  }, [runtime, collection, key, generation])

  return useMemo(
    () => ({ docs: state.docs, asOfSeq: state.asOfSeq, live, error }),
    [state, live, error],
  )
}
