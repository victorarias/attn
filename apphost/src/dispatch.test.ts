import { afterEach, describe, expect, test } from "bun:test"
import { mkdtemp, rm, writeFile } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { runDispatch, runReconcile } from "./dispatch.ts"
import type { RpcConnection } from "./rpc.ts"

const roots: string[] = []

afterEach(async () => {
  await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })))
})

async function bundle(source: string): Promise<string> {
  const root = await mkdtemp(join(tmpdir(), "attn-apphost-reconcile-"))
  roots.push(root)
  const artifact = join(root, "bundle.mjs")
  await writeFile(artifact, source)
  return artifact
}

describe("reconcile dispatch", () => {
  test("invokes the sibling export with the fixed reason and scoped current-state callback", async () => {
    const artifact = await bundle(`
      export default {
        reconcile: async (reason, ctx) => {
          const snapshot = await ctx.current.snapshot()
          await ctx.collections.items.put("receipt", { reason, snapshot })
        },
      }
    `)
    const calls: Array<{ method: string; params: unknown }> = []
    const notices: Array<{ method: string; params: unknown }> = []
    const conn = {
      call: async (method: string, params: unknown) => {
        calls.push({ method, params })
        if (method === "app.current.snapshot") return { asOfSeq: 41 }
        return { id: "receipt", body: {}, rev: 1 }
      },
      notify: (method: string, params: unknown) => notices.push({ method, params }),
    } as unknown as RpcConnection
    const reason = {
      causes: ["gap", "version_changed"] as const,
      version: 7,
      throughSeq: 41,
      gap: { cursor: 8, earliest: 12, missed: 3 },
      previousVersions: [3, 5],
    }

    const result = await runReconcile(conn, {
      dispatch: "dispatch-1",
      app: "reviewer",
      version_id: 7,
      artifact,
      collections: ["items"],
      reason: { ...reason, causes: [...reason.causes] },
    })

    expect(result).toEqual({ ok: true })
    expect(calls[0]).toEqual({
      method: "app.current.snapshot",
      params: { dispatch: "dispatch-1" },
    })
    expect(calls[1]?.method).toBe("app.collection.put")
    expect(calls[1]?.params).toEqual({
      dispatch: "dispatch-1",
      collection: "items",
      id: "receipt",
      body: { reason, snapshot: { asOfSeq: 41 } },
      if_rev: undefined,
    })
    expect(notices).toEqual([
      { method: "app_runtime.entered", params: { dispatch: "dispatch-1", app: "reviewer" } },
      { method: "app_runtime.left", params: { dispatch: "dispatch-1", app: "reviewer" } },
    ])
  })

  test("names a manifest and bundle that disagree about the sibling export", async () => {
    const artifact = await bundle(`export default { subscriptions: {} }`)
    const conn = { notify: () => {} } as unknown as RpcConnection
    const result = await runReconcile(conn, {
      dispatch: "dispatch-2",
      app: "reviewer",
      version_id: 9,
      artifact,
      collections: [],
      reason: { causes: ["re_enabled"], version: 9, throughSeq: 2, previousVersions: [] },
    })
    expect(result.ok).toBe(false)
    expect(result.error).toContain("default export has no reconcile handler")
  })
})

describe("ordinary handler context", () => {
  test("exposes the same current-state snapshot callback", async () => {
    const artifact = await bundle(`
      export default {
        subscriptions: {
          "ticket.updated": async (_event, ctx) => {
            await ctx.current.snapshot()
          },
        },
      }
    `)
    const calls: Array<{ method: string; params: unknown }> = []
    const conn = {
      call: async (method: string, params: unknown) => {
        calls.push({ method, params })
        return { asOfSeq: 9 }
      },
      notify: () => {},
    } as unknown as RpcConnection

    const result = await runDispatch(conn, {
      dispatch: "dispatch-event",
      app: "reviewer",
      version_id: 2,
      artifact,
      handler: "ticket.updated",
      collections: [],
      event: {
        name: "ticket.updated",
        subject: "t-1",
        seq: 9,
        payload: null,
        published_at: "2026-08-15T00:00:00Z",
      },
    })

    expect(result).toEqual({ ok: true })
    expect(calls).toEqual([
      { method: "app.current.snapshot", params: { dispatch: "dispatch-event" } },
    ])
  })
})
