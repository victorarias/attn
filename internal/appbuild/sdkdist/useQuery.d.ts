import type { Document, Filter } from "./index";
/** What a live query takes. No `after`: a live query is a window, a cursor is a walk. */
export interface LiveQueryOptions {
    filters?: Filter[];
    sort?: {
        field: string;
        desc?: boolean;
    };
    /** Defaults to the store's own 100, and refuses more than 1000. */
    limit?: number;
}
export interface QueryError {
    /**
     * What to act on. `collection_undefined` and `collection_redeclared` mean this
     * query is over; `invalid_query`, `undeclared_collection` and
     * `subscription_limit` mean it never started.
     */
    code: string;
    message: string;
}
export interface QueryResult<Body> {
    /** The window, in the server's order. */
    docs: Array<Document<Body>>;
    /** The log position this window was true at. Opaque and monotonic. */
    asOfSeq: number;
    /** Whether the daemon is serving this query right now. */
    live: boolean;
    /**
     * Set when the subscription ended and will not resume on its own. It is a
     * state to render, not an exception: a tile that spins forever because its
     * collection was removed is worse than one that says so.
     */
    error: QueryError | null;
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
export declare function useQuery<Body = unknown>(collection: string, options?: LiveQueryOptions): QueryResult<Body>;
