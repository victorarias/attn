// @types/node isn't a direct dependency of this package (only a transitive peer
// of vite/vitest), matching terminalOsc133.parity.test.ts's pattern.
// @ts-expect-error -- see above
import { readFileSync } from 'node:fs';
import type { ReactElement } from 'react';
import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import {
  Button,
  EmptyState,
  List,
  ListRow,
  Markdown,
  TextArea,
  TextInput,
} from '@victorarias/attn-app';

// The SDK's components emit class names; the stylesheet that gives them meaning
// lives here, in attn's build. Nothing links the two at compile time, and the
// failure is silent — a view renders, looks foreign, and no test notices.
//
// So this renders every component in every state that changes its classes, and
// fails on one the stylesheet does not define.

// Read rather than imported: vitest stubs a CSS import, and this test is about
// the file's contents. Relative to the vitest root (app/); import.meta.url is
// not a file: URL here.
const css: string = readFileSync('src/components/appViews/appSdkComponents.css', 'utf8');

const defined = new Set(
  [...css.matchAll(/\.(attn-app-[a-z0-9-]+)/g)].map((match) => match[1]),
);

function classesRendered(ui: ReactElement): string[] {
  const { container, unmount } = render(ui);
  const found = new Set<string>();
  for (const element of container.querySelectorAll('*')) {
    for (const name of element.classList) {
      if (name.startsWith('attn-app-')) found.add(name);
    }
  }
  unmount();
  return [...found];
}

describe('the SDK component slice', () => {
  const cases: Array<[string, ReactElement]> = [
    ['Button primary', <Button variant="primary">Approve</Button>],
    ['Button secondary', <Button>Later</Button>],
    ['Button danger', <Button variant="danger">Reject</Button>],
    ['TextInput', <TextInput value="" onChange={() => {}} />],
    ['TextInput invalid', <TextInput value="" onChange={() => {}} error="Say why" />],
    ['TextArea', <TextArea value="" onChange={() => {}} />],
    ['TextArea invalid', <TextArea value="" onChange={() => {}} error="Say why" />],
    [
      'List with rows',
      <List>
        <ListRow title="Static row" />
        <ListRow title="Clickable" meta="2 minutes" selected onClick={() => {}} actions={<Button>Go</Button>} />
      </List>,
    ],
    ['EmptyState', <EmptyState title="Nothing waiting" hint="reconnecting" />],
    ['Markdown', <Markdown>{'# Title\n\nsome **text**'}</Markdown>],
  ];

  for (const [name, ui] of cases) {
    it(`${name} renders only classes the stylesheet defines`, () => {
      const rendered = classesRendered(ui);
      expect(rendered.length).toBeGreaterThan(0);
      expect(rendered.filter((cls) => !defined.has(cls))).toEqual([]);
    });
  }

  it('covers every class the stylesheet defines', () => {
    // The other direction: a rule left behind when a component stops emitting
    // its class is dead weight nobody would ever notice.
    const rendered = new Set(cases.flatMap(([, ui]) => classesRendered(ui)));
    expect([...defined].filter((cls) => !rendered.has(cls)).sort()).toEqual([]);
  });
});
