import { describe, expect, it } from "vitest"
import * as React from "react"
import * as sdk from "@victorarias/attn-app"
import * as sdkJsx from "@victorarias/attn-app/jsx-runtime"
import * as reactJsx from "react/jsx-runtime"

// The one property the whole SDK-packaging design rests on: an app's component
// and attn's UI share a React *instance*. Two instances share no hook
// dispatcher, so the second one's useState throws on first render — a failure
// that reads as a bug in the app's code and is not.
//
// It holds here by construction of the build graph: sdk/attn-app is a workspace
// package of this app, so pnpm links both to one copy and the bundler resolves
// through the symlink to one module. That is exactly the kind of property a
// dependency bump can silently break, which is why it is asserted rather than
// argued.
describe("the app SDK's React", () => {
  it("is the same module instance the frontend uses", () => {
    expect(sdk.useState).toBe(React.useState)
    expect(sdk.useEffect).toBe(React.useEffect)
    expect(sdk.useMemo).toBe(React.useMemo)
    expect(sdk.useCallback).toBe(React.useCallback)
    expect(sdk.useRef).toBe(React.useRef)
    expect(sdk.useReducer).toBe(React.useReducer)
    expect(sdk.Fragment).toBe(React.Fragment)
  })

  it("hands a view the same JSX runtime attn's own components compile against", () => {
    expect(sdkJsx.jsx).toBe(reactJsx.jsx)
    expect(sdkJsx.jsxs).toBe(reactJsx.jsxs)
  })

  // The re-export list is the SDK's promise, and `export *` would have made
  // React's whole surface that promise. This is the list, stated once.
  it("re-exports React by name, not wholesale", () => {
    const reactNames = Object.keys(sdk).filter((name) => name in React)
    expect(reactNames.sort()).toEqual([
      "Fragment",
      "useCallback",
      "useEffect",
      "useMemo",
      "useReducer",
      "useRef",
      "useState",
    ])
  })
})
