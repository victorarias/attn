import { readFileSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { describe, expect, it } from "vitest"

// The bundle URL is composed here and parsed in Go, and neither side can see the
// other. A prefix that drifts is not a type error anywhere — it is a 404 at
// mount, with the daemon reporting a path nobody asked for. So the two strings
// are read off both sources and compared.

const read = (path) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8")

const frontend = read("./appBundle.ts")
const goRoute = read("../../../internal/daemon/app_bundle.go")
const goApps = read("../../../internal/apps/apps.go")

const goConst = (source, name) => source.match(new RegExp(`${name} = "([^"]*)"`))[1]
const tsConst = (name) => frontend.match(new RegExp(`${name} = '([^']*)'`))[1]

describe("the app bundle URL", () => {
  it("uses the route prefix the daemon serves", () => {
    expect(tsConst("APP_BUNDLE_ROUTE_PREFIX")).toBe(goConst(goRoute, "appBundleRoutePrefix"))
  })

  it("uses the tile-kind prefix the CLI prints and the layout stores", () => {
    // ViewTileKindPrefix is `= ConsumerPrefix` in Go, so the literal is one hop
    // away — which is the point: the prefix an app's consumer and its tiles share
    // is one string, not two that happen to match.
    expect(tsConst("APP_VIEW_TILE_KIND_PREFIX")).toBe(goConst(goApps, "ConsumerPrefix"))
  })
})
