import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

// sdk/attn-app/src/currentState.ts hand-copies the wire's current-state shapes.
// It has to: the SDK is its own package, published as declarations an app
// typechecks against with no npm, so it cannot import the frontend's generated
// types — and quicktype's output is neither readonly nor documented, which is
// what an author reads.
//
// A hand-copy with nothing watching it drifts silently: check-types and
// check-sdk both stay green while a field the daemon stopped sending is still
// promised to every app. This is what watches it. It compares field names and
// optionality against generated.ts, which is the file the daemon's own schema
// produces — a field added, removed, renamed or made optional on the wire fails
// here by name. Types are compared by presence, not by spelling: the SDK writes
// unions where quicktype writes enums, on purpose.

// Vitest runs with the app directory as its root, so the SDK is one level up.
const sdkSource = readFileSync(resolve(process.cwd(), '../sdk/attn-app/src/currentState.ts'), 'utf8');
const wireSource = readFileSync(resolve(process.cwd(), 'src/types/generated.ts'), 'utf8');

// The SDK's name for each shape, and the wire's. They agree except where the
// generated name carries a prefix the SDK has no use for.
// Plain JS, per the include rule in vite.config.ts: this reads source off disk
// and the app tsconfig carries no node types.
const SHAPES = [
  ['Session', 'Session'],
  ['EndpointCapabilities', 'EndpointCapabilities'],
  ['EndpointInfo', 'EndpointInfo'],
  ['WorkspacePane', 'WorkspaceLayoutPane'],
  ['WorkspaceLayout', 'WorkspaceLayout'],
  ['Workspace', 'Workspace'],
  ['PR', 'PR'],
  ['RepoState', 'RepoState'],
  ['AuthorState', 'AuthorState'],
  ['TicketRow', 'TicketRow'],
  ['SeedEdge', 'SeedEdge'],
  ['SeedPlotProgress', 'SeedPlotProgress'],
  ['SeedVar', 'SeedVar'],
  ['Seed', 'Seed'],
  ['CrewMember', 'CrewMember'],
  ['AppViewInfo', 'AppViewInfo'],
  ['AppRegistryEntry', 'AppRegistryEntry'],
];

/** Every declared field of one interface, as `name` or `name?`, sorted. */
function fieldsOf(source, name) {
  const header = `export interface ${name} {`;
  const start = source.indexOf(header);
  if (start < 0) throw new Error(`no interface ${name} in the source`);
  const end = source.indexOf('\n}', start);
  const body = source.slice(start + header.length, end);
  const fields = [];
  for (const line of body.split('\n')) {
    // The index signature quicktype emits is not a field.
    if (line.includes('[property: string]')) continue;
    const match = /^\s*(?:readonly\s+)?([A-Za-z_][A-Za-z0-9_]*)(\??):/.exec(line);
    if (match) fields.push(`${match[1]}${match[2]}`);
  }
  if (fields.length === 0) throw new Error(`interface ${name} parsed to no fields`);
  return fields.sort();
}

describe('the SDK current-state types', () => {
  it.each(SHAPES)('%s carries exactly what the wire sends', (sdkName, wireName) => {
    expect(fieldsOf(sdkSource, sdkName)).toEqual(fieldsOf(wireSource, wireName));
  });
});
