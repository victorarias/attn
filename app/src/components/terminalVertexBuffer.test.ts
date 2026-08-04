import { describe, expect, it } from 'vitest';
import {
  TERMINAL_FLOATS_PER_QUAD,
  TerminalVertexBuffer,
} from './terminalVertexBuffer';

describe('TerminalVertexBuffer', () => {
  it('writes the two triangles of a quad in GPU-ready layout', () => {
    const vertices = new TerminalVertexBuffer();
    vertices.pushQuad(10, 20, 30, 40, 0.1, 0.2, 0.7, 0.8, { r: 255, g: 128, b: 0 }, 0.5, 1);

    expect(vertices.length).toBe(TERMINAL_FLOATS_PER_QUAD);
    expect(vertices.quadCount).toBe(1);
    expect(Array.from(vertices.view().slice(0, 9))).toEqual([
      10, 20, expect.closeTo(0.1), expect.closeTo(0.2), 1, expect.closeTo(128 / 255), 0, 0.5, 1,
    ]);
    expect(Array.from(vertices.view().slice(-9))).toEqual([
      40, 60, expect.closeTo(0.7), expect.closeTo(0.8), 1, expect.closeTo(128 / 255), 0, 0.5, 1,
    ]);
  });

  it('accepts packed RGB without a color object', () => {
    const vertices = new TerminalVertexBuffer(TERMINAL_FLOATS_PER_QUAD);

    vertices.pushQuad(0, 0, 1, 1, 0, 0, 1, 1, 0xff8000, 0.5, 1);

    expect(Array.from(vertices.view().slice(4, 9))).toEqual([
      1, expect.closeTo(128 / 255), 0, 0.5, 1,
    ]);
  });

  it('reuses its allocation after reset', () => {
    const vertices = new TerminalVertexBuffer();
    vertices.pushQuad(0, 0, 1, 1, 0, 0, 1, 1, { r: 0, g: 0, b: 0 }, 1, 0);
    const allocation = vertices.view().buffer;

    vertices.reset();
    vertices.pushQuad(1, 1, 2, 2, 0, 0, 1, 1, { r: 255, g: 255, b: 255 }, 1, 0);

    expect(vertices.view().buffer).toBe(allocation);
    expect(vertices.quadCount).toBe(1);
  });

  it('grows without losing vertices already written in the frame', () => {
    const vertices = new TerminalVertexBuffer(TERMINAL_FLOATS_PER_QUAD);
    vertices.pushQuad(1, 2, 3, 4, 0, 0, 1, 1, { r: 1, g: 2, b: 3 }, 1, 0);
    const firstQuad = Array.from(vertices.view());
    vertices.pushQuad(5, 6, 7, 8, 0, 0, 1, 1, { r: 4, g: 5, b: 6 }, 1, 0);

    expect(vertices.quadCount).toBe(2);
    expect(Array.from(vertices.view().slice(0, TERMINAL_FLOATS_PER_QUAD))).toEqual(firstQuad);
  });

  it('appends an existing row without rebuilding its quads', () => {
    const row = new TerminalVertexBuffer(TERMINAL_FLOATS_PER_QUAD);
    row.pushQuad(1, 2, 3, 4, 0, 0, 1, 1, 0x010203, 1, 0);
    const frame = new TerminalVertexBuffer(TERMINAL_FLOATS_PER_QUAD);

    frame.append(row.view());
    frame.append(row.view());

    expect(frame.quadCount).toBe(2);
    expect(Array.from(frame.view().slice(0, TERMINAL_FLOATS_PER_QUAD))).toEqual(Array.from(row.view()));
    expect(Array.from(frame.view().slice(TERMINAL_FLOATS_PER_QUAD))).toEqual(Array.from(row.view()));
  });
});
