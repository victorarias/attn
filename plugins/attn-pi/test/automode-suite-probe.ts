// Loads an extension entrypoint (argv[2]) the way pi does — a default export
// called with pi's API — and prints what it registered.
//
// A subprocess rather than an import: an entrypoint reads its environment once
// at module scope, which is exactly the behavior under test, and a module
// loaded twice in one bun process reads it once.
export {};

const registered = { events: [] as string[], commands: [] as string[], flags: [] as string[] };

const pi = {
  on(event: string): void {
    registered.events.push(event);
  },
  registerCommand(name: string): void {
    registered.commands.push(name);
  },
  registerFlag(name: string): void {
    registered.flags.push(name);
  },
  getFlag(): undefined {
    return undefined;
  },
  sendUserMessage(): void {},
};

const entrypoint = process.argv[2];
if (!entrypoint) throw new Error("usage: automode-suite-probe.ts <entrypoint>");
const { default: factory } = await import(entrypoint);
(factory as (pi: unknown) => void)(pi);

console.log(JSON.stringify(registered));
