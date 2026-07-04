import { describe, expect, it } from 'vitest';
import { resolveCaptureRect } from './nativeWindowCapture.mjs';
import { parseCropSpec } from './capture-app-screenshot.mjs';

describe('resolveCaptureRect', () => {
  const windowBounds = { x: 100, y: 200, width: 800, height: 600 };

  it('returns the full window rect when no crop is given', () => {
    expect(resolveCaptureRect(windowBounds, null)).toEqual({
      x: 100,
      y: 200,
      width: 800,
      height: 600,
    });
  });

  it('offsets a fully-contained crop by the window origin', () => {
    expect(resolveCaptureRect(windowBounds, { x: 10, y: 20, width: 300, height: 150 })).toEqual({
      x: 110,
      y: 220,
      width: 300,
      height: 150,
    });
  });

  it('clamps a crop that spills past the right/bottom edge', () => {
    expect(resolveCaptureRect(windowBounds, { x: 700, y: 500, width: 300, height: 300 })).toEqual({
      x: 800,
      y: 700,
      width: 100,
      height: 100,
    });
  });

  it('clamps a crop that starts before the window origin', () => {
    expect(resolveCaptureRect(windowBounds, { x: -50, y: -50, width: 100, height: 100 })).toEqual({
      x: 100,
      y: 200,
      width: 50,
      height: 50,
    });
  });

  it('throws when the crop rect does not overlap the window at all', () => {
    expect(() =>
      resolveCaptureRect(windowBounds, { x: 1000, y: 1000, width: 50, height: 50 }),
    ).toThrow(/does not overlap/);
  });

  it('throws on a non-finite or non-positive crop rect', () => {
    expect(() => resolveCaptureRect(windowBounds, { x: 0, y: 0, width: 0, height: 10 })).toThrow(
      /Invalid crop rect/,
    );
    expect(() =>
      resolveCaptureRect(windowBounds, { x: 'a', y: 0, width: 10, height: 10 }),
    ).toThrow(/Invalid crop rect/);
  });

  it('throws when windowBounds is missing', () => {
    expect(() => resolveCaptureRect(null, null)).toThrow('resolveCaptureRect requires windowBounds');
  });
});

describe('parseCropSpec', () => {
  it('parses the "x,y,WxH" form', () => {
    expect(parseCropSpec('0,0,800x600')).toEqual({ x: 0, y: 0, width: 800, height: 600 });
  });

  it('parses the "x,y,w,h" all-comma form', () => {
    expect(parseCropSpec('10,20,300,150')).toEqual({ x: 10, y: 20, width: 300, height: 150 });
  });

  it('parses negative offsets', () => {
    expect(parseCropSpec('-10,-20,300x150')).toEqual({ x: -10, y: -20, width: 300, height: 150 });
  });

  it('rejects garbage input', () => {
    expect(() => parseCropSpec('not-a-crop')).toThrow(/Invalid --crop value/);
    expect(() => parseCropSpec('0,0')).toThrow(/Invalid --crop value/);
    expect(() => parseCropSpec('0,0,0x0')).toThrow(/Invalid --crop value/);
    expect(() => parseCropSpec('0,0,800xabc')).toThrow(/Invalid --crop value/);
    expect(() => parseCropSpec('')).toThrow(/Invalid --crop value/);
  });
});
