/**
 * Builds a throwaway git repository that produces a realistic, controlled diff
 * for exercising the review/diff panel (DiffView over @pierre/diffs) in the
 * packaged app.
 *
 * The repo has a `main` baseline pushed to a local bare `origin` (so the daemon
 * resolves `origin/HEAD` -> `origin/main` and diffs `origin/main...HEAD`), then
 * a `feature` branch whose committed changes span the cases that matter for the
 * renderer:
 *   - multiple languages (Shiki highlighting): .ts, .py, .go, .css, .md
 *   - a pure addition, a pure deletion, and several modifications
 *   - a file with two separated hunks (collapsed-context behavior)
 *   - a large file (virtualization)
 * Plus one uncommitted edit, so the working-tree path is exercised too.
 */
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

const GIT_ENV = {
  GIT_AUTHOR_NAME: 'Attn Harness',
  GIT_AUTHOR_EMAIL: 'harness@attn.test',
  GIT_COMMITTER_NAME: 'Attn Harness',
  GIT_COMMITTER_EMAIL: 'harness@attn.test',
};

function git(cwd, ...args) {
  return execFileSync('git', args, {
    cwd,
    env: { ...process.env, ...GIT_ENV },
    stdio: ['ignore', 'pipe', 'pipe'],
  }).toString();
}

function write(repoDir, relPath, contents) {
  const full = path.join(repoDir, relPath);
  fs.mkdirSync(path.dirname(full), { recursive: true });
  fs.writeFileSync(full, contents, 'utf8');
}

function remove(repoDir, relPath) {
  fs.rmSync(path.join(repoDir, relPath), { force: true });
}

function bigFile(marker) {
  const lines = [];
  lines.push('// Auto-generated lookup table (large file for virtualization).');
  lines.push('export const TABLE: Record<string, number> = {');
  for (let i = 0; i < 240; i += 1) {
    lines.push(`  entry_${String(i).padStart(3, '0')}: ${marker === 'feature' && i === 120 ? 9999 : i},`);
  }
  lines.push('};');
  return `${lines.join('\n')}\n`;
}

/**
 * @param {string} rootDir directory under which the fixture repo + its bare origin are created
 * @returns {{ repoDir: string, originDir: string }}
 */
export function buildDiffFixtureRepo(rootDir) {
  const repoDir = path.join(rootDir, 'fixture-repo');
  const originDir = path.join(rootDir, 'fixture-origin.git');
  fs.mkdirSync(repoDir, { recursive: true });

  // --- main baseline -------------------------------------------------------
  git(repoDir, 'init', '-q');
  git(repoDir, 'checkout', '-q', '-b', 'main');

  write(repoDir, 'src/app.ts', [
    "import { greet } from './util';",
    '',
    'export function main(name: string): string {',
    '  const message = greet(name);',
    '  console.log(message);',
    '  return message;',
    '}',
    '',
    'export function add(a: number, b: number): number {',
    '  return a + b;',
    '}',
    '',
    'export function subtract(a: number, b: number): number {',
    '  return a - b;',
    '}',
    '',
    'export const VERSION = "1.0.0";',
    '',
  ].join('\n'));

  write(repoDir, 'src/legacy.py', [
    '"""Legacy helper scheduled for removal."""',
    '',
    'def fib(n):',
    '    a, b = 0, 1',
    '    for _ in range(n):',
    '        a, b = b, a + b',
    '    return a',
    '',
  ].join('\n'));

  write(repoDir, 'styles/main.css', [
    '.button {',
    '  color: #111;',
    '  padding: 4px 8px;',
    '}',
    '',
    '.button:hover {',
    '  color: #000;',
    '}',
    '',
  ].join('\n'));

  write(repoDir, 'README.md', [
    '# Fixture Project',
    '',
    'A throwaway project used to exercise the diff viewer.',
    '',
    '## Usage',
    '',
    'Nothing to see here.',
    '',
  ].join('\n'));

  write(repoDir, 'data/table.ts', bigFile('main'));

  git(repoDir, 'add', '-A');
  git(repoDir, 'commit', '-q', '-m', 'baseline');

  // --- local bare origin so origin/main resolves --------------------------
  fs.mkdirSync(originDir, { recursive: true });
  git(originDir, 'init', '-q', '--bare');
  git(repoDir, 'remote', 'add', 'origin', originDir);
  git(repoDir, 'push', '-q', 'origin', 'main');
  // Point origin/HEAD at main so GetDefaultBranch() reports "main".
  git(repoDir, 'remote', 'set-head', 'origin', 'main');

  // --- feature branch: committed changes ----------------------------------
  git(repoDir, 'checkout', '-q', '-b', 'feature');

  // Modify src/app.ts in two separated regions -> two hunks.
  write(repoDir, 'src/app.ts', [
    "import { greet } from './util';",
    "import { farewell } from './util';",
    '',
    'export function main(name: string): string {',
    '  const message = greet(name);',
    '  console.log(message);',
    '  console.log(farewell(name));',
    '  return message;',
    '}',
    '',
    'export function add(a: number, b: number): number {',
    '  return a + b;',
    '}',
    '',
    'export function subtract(a: number, b: number): number {',
    '  return a - b;',
    '}',
    '',
    'export const VERSION = "2.0.0";',
    '',
  ].join('\n'));

  // Modify CSS and README.
  write(repoDir, 'styles/main.css', [
    '.button {',
    '  color: #2563eb;',
    '  padding: 6px 12px;',
    '  border-radius: 6px;',
    '}',
    '',
    '.button:hover {',
    '  color: #1d4ed8;',
    '}',
    '',
  ].join('\n'));

  write(repoDir, 'README.md', [
    '# Fixture Project',
    '',
    'A throwaway project used to exercise the diff viewer.',
    '',
    '## Usage',
    '',
    'Run `main("world")` to greet the world.',
    '',
    '## License',
    '',
    'MIT',
    '',
  ].join('\n'));

  // Large-file modification (single hunk deep in a big file).
  write(repoDir, 'data/table.ts', bigFile('feature'));

  // Pure addition: a new Go file (distinct language) + a new TS util.
  write(repoDir, 'src/feature.go', [
    'package main',
    '',
    'import "fmt"',
    '',
    'func Feature(name string) string {',
    '\treturn fmt.Sprintf("feature: %s", name)',
    '}',
    '',
  ].join('\n'));

  write(repoDir, 'src/util.ts', [
    'export function greet(name: string): string {',
    '  return `Hello, ${name}!`;',
    '}',
    '',
    'export function farewell(name: string): string {',
    '  return `Goodbye, ${name}.`;',
    '}',
    '',
  ].join('\n'));

  // Pure deletion.
  remove(repoDir, 'src/legacy.py');

  git(repoDir, 'add', '-A');
  git(repoDir, 'commit', '-q', '-m', 'feature changes');

  // --- one uncommitted edit (working-tree path) ---------------------------
  write(repoDir, 'src/util.ts', [
    'export function greet(name: string): string {',
    '  // tweaked while reviewing',
    '  return `Hello, ${name}!`;',
    '}',
    '',
    'export function farewell(name: string): string {',
    '  return `Goodbye, ${name}.`;',
    '}',
    '',
  ].join('\n'));

  return { repoDir, originDir };
}
