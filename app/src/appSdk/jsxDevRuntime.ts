// `@victorarias/attn-app/jsx-dev-runtime` — see ./index.ts.
//
// `attn app apply` builds a view in production mode, so nothing shipped reaches
// this specifier. It is mapped anyway: a specifier the SDK exports and the map
// does not resolve is a bare-specifier link error at mount, and that failure is
// far harder to read than an unused entry is to carry.
export * from "@victorarias/attn-app/jsx-dev-runtime"
