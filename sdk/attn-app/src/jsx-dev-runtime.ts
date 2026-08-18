// The development JSX runtime.
//
// Bundlers reach for this one unless they are told to build for production, so
// the SDK carries it rather than leaving a default flag to fail a build. A
// stored app version is immutable and content-addressed — there is no per-run
// dev/prod split — so a view is built in production mode and this is what keeps
// the failure mode a resolved import instead of a missing one.

export { Fragment, jsxDEV } from "react/jsx-dev-runtime"
export type { JSX } from "react/jsx-dev-runtime"
