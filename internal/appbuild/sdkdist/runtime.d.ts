/** One document a resuming subscriber already holds, and at which revision. */
export interface DocumentRevision {
    id: string;
    rev: number;
}
/** A stored document exactly as the wire carries it: the body is still JSON text. */
export interface RawDocument {
    id: string;
    body: string;
    rev: number;
    created_at: string;
    updated_at: string;
}
/** A query a live subscription can answer. No cursor: a live query is a window. */
export interface LiveQueryRequest {
    namespace: string;
    collection: string;
    filters?: Array<{
        field: string;
        op: string;
        value: unknown;
    }>;
    sort?: {
        field: string;
        desc?: boolean;
    };
    limit?: number;
}
/**
 * One delivery, and the whole client contract is one rule:
 *
 *   Render `order`. Take each body from `upsert` if it is there, else from your
 *   cache. Forget every cached document not named in `order`.
 */
export interface QueryDelivery {
    delivery: number;
    asOfSeq: number;
    order: string[];
    upsert: RawDocument[];
}
/** What the host calls back as a subscription runs. */
export interface DocumentSubscriber {
    request: LiveQueryRequest;
    /** What this subscriber holds, read by the host at every subscribe and resubscribe. */
    have: () => DocumentRevision[];
    onDelivery: (delivery: QueryDelivery) => void;
    /** Terminal: the daemon will not answer this query again as written. */
    onEnded: (code: string, message: string) => void;
    /** Whether the daemon is serving this subscription right now. */
    onLive: (live: boolean) => void;
}
/** What the host provides to everything it mounts. */
export interface AppViewRuntime {
    /** The document namespace this mount reads. A view cannot widen it. */
    readonly namespace: string;
    /** Opens a live query; the returned function closes it and must be called. */
    subscribe: (subscriber: DocumentSubscriber) => () => void;
}
/** The host wraps every mounted view in this. */
export declare const AppViewRuntimeProvider: import("react").Provider<AppViewRuntime | null>;
/**
 * The runtime this view is mounted in. Null outside a host — which is a real
 * state for a component rendered in an app's own tests, so the hooks report it
 * rather than throwing.
 */
export declare function useAppViewRuntime(): AppViewRuntime | null;
