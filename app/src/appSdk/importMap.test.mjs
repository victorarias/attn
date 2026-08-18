import { readFileSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { describe, expect, it } from "vitest"

// The import map is a join nobody can see whole. Four surfaces have to agree on
// one list of specifiers, and each one is in a different language:
//
//   - the SDK package's exports map (what an app may import),
//   - internal/appbuild's SDKSpecifiers (what the view build marks external),
//   - index.html's import map (what the browser resolves those to),
//   - vite.config.ts's fixed-name chunks (what those URLs are).
//
// A specifier missing from any one of them is a view that mounts and then fails
// to link, with an error naming a bare module specifier and nothing else. So the
// list is read off all four and compared, rather than restated in a fifth place.

const read = (path) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8")

const sdkPackage = JSON.parse(read("../../../sdk/attn-app/package.json"))
const indexHtml = read("../../index.html")
const viteConfig = read("../../vite.config.ts")
const appbuildCodegen = read("../../../internal/appbuild/codegen.go")

const importMap = JSON.parse(
  indexHtml.match(/<script type="importmap">([\s\S]*?)<\/script>/)[1],
).imports

const sdkSpecifiers = Object.keys(sdkPackage.exports).map((key) =>
  key === "." ? sdkPackage.name : sdkPackage.name + key.slice(1),
)

describe("the app SDK's import map", () => {
  it("resolves every specifier the SDK package exports", () => {
    expect(Object.keys(importMap).sort()).toEqual(sdkSpecifiers.sort())
  })

  it("resolves every specifier the view build marks external", () => {
    // SDKSpecifiers() in Go, read as source: the two sides cannot import each
    // other, and a Go change that adds a surface has to fail here.
    const goList = appbuildCodegen.match(/func SDKSpecifiers\(\) \[\]string \{[\s\S]*?\n\}/)[0]
    const suffixes = [...goList.matchAll(/SDKModule \+ "([^"]+)"/g)].map((m) => m[1])
    const external = [sdkPackage.name, ...suffixes.map((s) => sdkPackage.name + s)]
    expect(external.sort()).toEqual(sdkSpecifiers.sort())
  })

  it("points every specifier at a chunk the build emits under a fixed name", () => {
    const chunks = viteConfig.match(/const APP_SDK_CHUNKS[\s\S]*?\n\};/)[0]
    const names = [...chunks.matchAll(/"([a-z0-9-]+)":/g)].map((m) => m[1])
    expect(Object.values(importMap).sort()).toEqual(names.map((n) => `/${n}.js`).sort())
  })
})
