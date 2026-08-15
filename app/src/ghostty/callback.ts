// Installing a JS function as a wasm function pointer.
//
// libghostty-vt takes callbacks (write_pty, bell, …) as indices into the
// module's exported __indirect_function_table. The standard way to put a JS
// function there is `new WebAssembly.Function(...)` from the js-types proposal —
// which JavaScriptCore does not implement, and JavaScriptCore is what WKWebView
// runs. So instead we hand-assemble a tiny wasm module that imports a JS
// function and re-exports it as a wasm one, which every engine supports.
//
// The shim's signature is fixed at (i32, i32, i32, i32) -> (), matching
// GhosttyTerminalWritePtyFn(terminal, userdata, data, len) — the only callback
// the browser model installs.

function uleb(n: number): number[] {
  const out: number[] = [];
  do {
    let byte = n & 0x7f;
    n >>>= 7;
    if (n) byte |= 0x80;
    out.push(byte);
  } while (n);
  return out;
}

function section(id: number, payload: number[]): number[] {
  return [id, ...uleb(payload.length), ...payload];
}

function name(text: string): number[] {
  const bytes = [...new TextEncoder().encode(text)];
  return [...uleb(bytes.length), ...bytes];
}

// (func (param i32 i32 i32 i32))
const FUNC_TYPE = [0x60, ...uleb(4), 0x7f, 0x7f, 0x7f, 0x7f, ...uleb(0)];
// no locals; forward all four params to the imported function and return.
const CODE = [0x00, 0x20, 0x00, 0x20, 0x01, 0x20, 0x02, 0x20, 0x03, 0x10, 0x00, 0x0b];

const SHIM_WASM = new Uint8Array([
  0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
  ...section(1, [...uleb(1), ...FUNC_TYPE]),
  ...section(2, [...uleb(1), ...name('env'), ...name('cb'), 0x00, ...uleb(0)]),
  ...section(3, [...uleb(1), ...uleb(0)]),
  ...section(7, [...uleb(1), ...name('f'), 0x00, ...uleb(1)]),
  ...section(10, [...uleb(1), ...uleb(CODE.length), ...CODE]),
]);

let shimModule: WebAssembly.Module | null = null;

export type Callback4 = (a: number, b: number, c: number, d: number) => void;

/**
 * Install `fn` in the module's function table and return its index, for use as
 * a C function pointer. The entry is never reclaimed: a terminal installs one
 * callback for its lifetime, and the table only grows.
 */
export function installCallback(
  table: WebAssembly.Table,
  fn: Callback4,
): number {
  shimModule ??= new WebAssembly.Module(SHIM_WASM);
  const shim = new WebAssembly.Instance(shimModule, { env: { cb: fn } });
  const index = table.grow(1);
  table.set(index, shim.exports.f as WebAssembly.ExportValue);
  return index;
}
