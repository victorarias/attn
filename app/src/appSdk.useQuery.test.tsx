import { describe, expect, it } from "vitest"
import { act, render, screen } from "@testing-library/react"
import {
  AppViewRuntimeProvider,
  useQuery,
  type AppViewRuntime,
  type DocumentSubscriber,
} from "@victorarias/attn-app"

// useQuery holds A3.4's delivery contract so a view never meets it. What is
// asserted here is that contract from the view's side: render `order`, take each
// body from `upsert` or the cache, forget the rest — and, across a remount,
// resume by declaring what is still held so only what changed travels.

type Body = { status: string }

function doc(id: string, rev: number, status: string) {
  return { id, body: JSON.stringify({ status }), rev, created_at: "", updated_at: "" }
}

/** A host stand-in that hands the test the subscriber the SDK registered. */
function fakeRuntime({ liveOnSubscribe = true }: { liveOnSubscribe?: boolean } = {}) {
  const subscribers: DocumentSubscriber[] = []
  let unsubscribes = 0
  const runtime: AppViewRuntime = {
    namespace: "app/approval-gate",
    subscribe: (subscriber) => {
      subscribers.push(subscriber)
      if (liveOnSubscribe) subscriber.onLive(true)
      return () => {
        unsubscribes += 1
      }
    },
    command: async () => undefined,
  }
  return {
    runtime,
    subscribers,
    latest: () => subscribers[subscribers.length - 1],
    unsubscribeCount: () => unsubscribes,
  }
}

function View({ collection }: { collection: string }) {
  const { docs, live, error, asOfSeq } = useQuery<Body>(collection, {
    filters: [{ field: "status", op: "eq", value: "pending" }],
  })
  if (error) return <div data-testid="error">{error.code}</div>
  return (
    <div>
      <div data-testid="live">{live ? "live" : "not live"}</div>
      <div data-testid="seq">{asOfSeq}</div>
      <div data-testid="ids">{docs.map((d) => `${d.id}:${d.body.status}`).join(",")}</div>
    </div>
  )
}

function mount(runtime: AppViewRuntime, collection = "requests") {
  return render(
    <AppViewRuntimeProvider value={runtime}>
      <View collection={collection} />
    </AppViewRuntimeProvider>,
  )
}

describe("useQuery", () => {
  it("renders the window in the server's order, from bodies it was sent", () => {
    const host = fakeRuntime()
    mount(host.runtime, "renders")

    act(() => {
      host.latest().onDelivery({
        delivery: 1,
        asOfSeq: 9,
        order: ["b", "a"],
        upsert: [doc("a", 1, "pending"), doc("b", 1, "pending")],
      })
    })

    expect(screen.getByTestId("ids").textContent).toBe("b:pending,a:pending")
    expect(screen.getByTestId("seq").textContent).toBe("9")
    expect(screen.getByTestId("live").textContent).toBe("live")
  })

  it("takes an unchanged body from its cache and forgets what left the window", () => {
    const host = fakeRuntime()
    mount(host.runtime, "forgets")

    act(() => {
      host.latest().onDelivery({
        delivery: 1,
        asOfSeq: 1,
        order: ["a", "b"],
        upsert: [doc("a", 1, "pending"), doc("b", 1, "pending")],
      })
    })
    // b left the window; a's body did not travel, because the view already holds it.
    act(() => {
      host.latest().onDelivery({ delivery: 2, asOfSeq: 2, order: ["a"], upsert: [] })
    })

    expect(screen.getByTestId("ids").textContent).toBe("a:pending")
    // The forget rule reaches the resume token too: b is no longer claimed.
    expect(host.latest().have()).toEqual([{ id: "a", rev: 1 }])
  })

  it("resumes a remount by declaring what it still holds", () => {
    const host = fakeRuntime()
    const first = mount(host.runtime, "resumes")

    act(() => {
      host.latest().onDelivery({
        delivery: 1,
        asOfSeq: 1,
        order: ["a"],
        upsert: [doc("a", 3, "pending")],
      })
    })
    first.unmount()
    expect(host.unsubscribeCount()).toBe(1)

    mount(host.runtime, "resumes")
    expect(host.latest().have()).toEqual([{ id: "a", rev: 3 }])
  })

  it("starts over when a delivery names a body nobody holds", () => {
    const host = fakeRuntime()
    mount(host.runtime, "invariant")

    act(() => {
      host.latest().onDelivery({
        delivery: 1,
        asOfSeq: 1,
        order: ["a"],
        upsert: [doc("a", 1, "pending")],
      })
    })
    const before = host.subscribers.length
    // A window ordering a document whose body was neither sent nor held: the
    // subscription is broken, and the remedy is a fresh one with no `have`.
    act(() => {
      host.latest().onDelivery({ delivery: 2, asOfSeq: 2, order: ["ghost"], upsert: [] })
    })

    expect(host.subscribers.length).toBe(before + 1)
    expect(host.latest().have()).toEqual([])
  })

  it("surfaces an ended subscription as a state the view can render", () => {
    const host = fakeRuntime()
    mount(host.runtime, "ends")

    act(() => {
      host.latest().onEnded("collection_undefined", "the collection was removed")
    })

    expect(screen.getByTestId("error").textContent).toBe("collection_undefined")
  })

  it("says so when the daemon is not serving the query right now", () => {
    const host = fakeRuntime()
    mount(host.runtime, "offline")

    act(() => {
      host.latest().onLive(false)
    })

    expect(screen.getByTestId("live").textContent).toBe("not live")
  })

  it("refuses a cursor rather than subscribing to a window that walks", () => {
    const host = fakeRuntime()
    function Cursor() {
      const { error } = useQuery("requests", { after: "a" } as never)
      return <div data-testid="error">{error?.code ?? ""}</div>
    }
    render(
      <AppViewRuntimeProvider value={host.runtime}>
        <Cursor />
      </AppViewRuntimeProvider>,
    )

    expect(screen.getByTestId("error").textContent).toBe("invalid_query")
    expect(host.subscribers).toHaveLength(0)
  })


  it("shows nothing of the previous query when the query changes", () => {
    // A view that changes its filter, sort or limit asks a different question.
    // Rendering the old answer under the new label — and calling it live — is the
    // one way a window can lie about what it is.
    const host = fakeRuntime({ liveOnSubscribe: false })
    function Limited({ limit }: { limit: number }) {
      const { docs, live, asOfSeq } = useQuery<Body>("changes", { limit })
      return (
        <div>
          <div data-testid="live">{live ? "live" : "not live"}</div>
          <div data-testid="seq">{asOfSeq}</div>
          <div data-testid="ids">{docs.map((d) => d.id).join(",")}</div>
        </div>
      )
    }
    const view = render(
      <AppViewRuntimeProvider value={host.runtime}>
        <Limited limit={10} />
      </AppViewRuntimeProvider>,
    )
    act(() => {
      host.latest().onLive(true)
      host.latest().onDelivery({
        delivery: 1,
        asOfSeq: 7,
        order: ["a"],
        upsert: [doc("a", 1, "pending")],
      })
    })
    expect(screen.getByTestId("ids").textContent).toBe("a")

    view.rerender(
      <AppViewRuntimeProvider value={host.runtime}>
        <Limited limit={20} />
      </AppViewRuntimeProvider>,
    )

    expect(screen.getByTestId("ids").textContent).toBe("")
    expect(screen.getByTestId("seq").textContent).toBe("0")
    expect(screen.getByTestId("live").textContent).toBe("not live")
    // The new question is asked with no bodies claimed: they answered the old one.
    expect(host.latest().have()).toEqual([])
  })

  it("keeps two mounts of the same query out of each other's bodies", () => {
    // The daemon credits `have` per subscription. One shared cache would let one
    // tile's forget-delete invalidate bodies the daemon still credits to the
    // other, so the second holder resumes nothing instead.
    const host = fakeRuntime()
    const first = mount(host.runtime, "shared")
    act(() => {
      host.latest().onDelivery({
        delivery: 1,
        asOfSeq: 1,
        order: ["a"],
        upsert: [doc("a", 4, "pending")],
      })
    })
    const firstSubscriber = host.latest()

    const second = mount(host.runtime, "shared")
    expect(host.latest().have()).toEqual([])
    // The first tile still holds what it was sent.
    expect(firstSubscriber.have()).toEqual([{ id: "a", rev: 4 }])

    second.unmount()
    first.unmount()
    // With both released, a remount resumes from the holder's cache again.
    mount(host.runtime, "shared")
    expect(host.latest().have()).toEqual([{ id: "a", rev: 4 }])
  })

  it("reports having no host rather than pretending to query one", () => {
    render(<View collection="hostless" />)
    expect(screen.getByTestId("error").textContent).toBe("no_runtime")
  })
})
