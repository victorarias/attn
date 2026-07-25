// @vitest-environment node
// Proves the shared OSC 133 SEGMENTER parity corpus against the client
// parseOsc133. The same corpus (internal/pty/testdata/osc133_segmenter_corpus
// .json) is consumed by internal/pty/osc133_test.go against the Go worker
// segmenter. Marker parity is the contract: the same bytes, fed the same way
// (split across chunks), must yield the same ordered markers — same cmdline
// decoding, same exit-code parsing — in both parsers. The client parser is the
// reference; the Go port is written to match it here.
//
// Segment/plain-byte layout is deliberately NOT part of this corpus: the client
// writes marker bytes through to the terminal while the worker strips them, so
// only the extracted markers are comparable across the two.
// @types/node isn't a direct dependency of this package (only a transitive peer
// of vite/vitest), matching terminalBlocks.corpus.test.ts's pattern.
// @ts-expect-error -- see above
import { readFileSync } from 'node:fs';
// @ts-expect-error -- see above
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { emptyOsc133State, parseOsc133, type Osc133Marker } from './terminalOsc133';

interface CorpusCase {
  name: string;
  chunks: string[];
  markers: Array<{
    kind: Osc133Marker['kind'];
    cmdline?: string;
    exitCode?: number;
  }>;
}

const corpusPath = fileURLToPath(
  new URL('../../../internal/pty/testdata/osc133_segmenter_corpus.json', import.meta.url),
);
const corpus = JSON.parse(readFileSync(corpusPath, 'utf8')) as { cases: CorpusCase[] };

const encoder = new TextEncoder();

// Feed the case's chunks sequentially through ONE parser state so split-across-
// chunk buffering is exercised, and collect the markers in emission order.
function collectMarkers(chunks: string[]): Osc133Marker[] {
  let state = emptyOsc133State();
  const markers: Osc133Marker[] = [];
  for (const chunk of chunks) {
    const result = parseOsc133(state, encoder.encode(chunk));
    state = result.state;
    for (const segment of result.segments) {
      if (segment.marker) markers.push(segment.marker);
    }
  }
  return markers;
}

// Normalize an extracted marker to the corpus's minimal shape: a field is
// present only when it carries a value, so an absent cmdline/exitCode in the
// corpus asserts the parser produced none.
function normalize(marker: Osc133Marker): CorpusCase['markers'][number] {
  const out: CorpusCase['markers'][number] = { kind: marker.kind };
  if (marker.kind === 'pre-exec' && marker.cmdline !== undefined) out.cmdline = marker.cmdline;
  if (marker.kind === 'command-end' && marker.exitCode !== undefined) out.exitCode = marker.exitCode;
  return out;
}

describe('parseOsc133 segmenter parity corpus', () => {
  it('covers every case', () => {
    expect(corpus.cases.length).toBeGreaterThan(0);
  });

  for (const testCase of corpus.cases) {
    it(testCase.name, () => {
      const markers = collectMarkers(testCase.chunks).map(normalize);
      expect(markers).toEqual(testCase.markers);
    });
  }
});
