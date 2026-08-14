export { Fragment, useCallback, useEffect, useMemo, useReducer, useRef, useState, } from "react";
export type { ReactElement, ReactNode } from "react";
/** The app contract version this SDK describes — attn-app.toml's attn_app_api. */
export declare const APP_API_VERSION = 1;
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
    readonly name: string;
    /** The entity the fact is about — a session id, a ticket id, an app name. */
    readonly subject: string;
    /** Position in the durable log. Opaque and monotonic; do not persist it as a cursor. */
    readonly seq: number;
    /**
     * The producer's payload, when it carries one. Typed as unknown by design:
     * the SDK gains a type per fact class as each one's shape is documented,
     * and claiming a shape it has not pinned would be worse than honest unknown.
     */
    readonly payload: unknown;
    /** When the fact was published, RFC3339. */
    readonly published_at: string;
}
/** One stored document. Body is what was written, byte for byte. */
export interface Document<Body = unknown> {
    readonly id: string;
    readonly body: Body;
    /**
     * Counts writes to this document. Hand it back to put() to refuse an edit
     * built on a version somebody else has already replaced.
     */
    readonly rev: number;
    readonly created_at: string;
    readonly updated_at: string;
}
/** The comparisons a declared field supports. */
export type FilterOp = "eq" | "lt" | "lte" | "gt" | "gte";
export interface Filter {
    /** A field this collection declared, or `created_at` / `updated_at`. */
    field: string;
    op: FilterOp;
    value: string | number | boolean;
}
export interface QueryOptions {
    filters?: Filter[];
    sort?: {
        field: string;
        desc?: boolean;
    };
    /** Defaults to 100, and refuses more than 1000. */
    limit?: number;
    /** The id of the previous page's last document. */
    after?: string;
}
export interface PutOptions {
    /**
     * The rev the caller read. The write is refused when the stored document
     * has moved on, which is how a read-modify-write stays safe without a lock.
     */
    ifRev?: number;
}
/**
 * One of the app's document collections, scoped to its own namespace. An app
 * can only ever address its own documents: the namespace is derived from the
 * app's name, not passed in.
 */
export interface Collection {
    get<Body = unknown>(id: string): Promise<Document<Body> | null>;
    put<Body = unknown>(id: string, body: Body, options?: PutOptions): Promise<Document<Body>>;
    delete(id: string): Promise<boolean>;
    query<Body = unknown>(options?: QueryOptions): Promise<Document<Body>[]>;
    count(options?: Pick<QueryOptions, "filters">): Promise<number>;
}
/** What a handler is given besides the fact. */
export interface AppContext<Collections> {
    /** This app's name — its registry key, its consumer, its namespace. */
    readonly app: string;
    /** The version id that is running, which a rollback does not rewrite. */
    readonly version: number;
    /** The collections declared in attn-app.toml, by name. */
    readonly collections: Collections;
}
/**
 * A handler. Throwing fails that delivery: the bus retries it with backoff
 * rather than skipping it, every attempt is recorded, and an app stuck on one
 * event long enough is disabled so a single broken app cannot hold the event
 * log open for everyone.
 */
export type Handler<Collections> = (event: AppEvent, ctx: AppContext<Collections>) => void | Promise<void>;
/**
 * What a view is given. A view is a function of where it sits: the first three
 * are ambient, and `params` is what the user typed when docking, which is what
 * makes two tiles of one view show different things.
 */
export interface ViewProps {
    /** The workspace this tile is in. */
    readonly workspaceId: string;
    /** The session that workspace has selected, if any. */
    readonly sessionId: string | null;
    /** Stable for the life of this docked tile. */
    readonly tileId: string;
    /** Opaque to attn — the app decides what it means. Empty when none was given. */
    readonly params: string;
}
export { useQuery } from "./useQuery";
export type { LiveQueryOptions, QueryError, QueryResult } from "./useQuery";
export { AppViewRuntimeProvider, useAppViewRuntime } from "./runtime";
export type { AppViewRuntime, DocumentRevision, DocumentSubscriber, LiveQueryRequest, QueryDelivery, RawDocument, } from "./runtime";
