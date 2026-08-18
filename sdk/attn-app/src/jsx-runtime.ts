// The JSX runtime an app's TSX compiles against.
//
// The scaffold's tsconfig sets `jsxImportSource` to the SDK, so `<Thing />` in a
// view becomes an import of this module rather than of React. That is what makes
// React a specifier an app cannot write, and what puts an app's elements on the
// same React instance as attn's own UI.

export { Fragment, jsx, jsxs } from "react/jsx-runtime"
export type { JSX } from "react/jsx-runtime"
