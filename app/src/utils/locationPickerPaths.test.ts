import { describe, expect, it } from 'vitest';
import { normalizePickerPath } from './locationPickerPaths';

describe('locationPickerPaths', () => {
  it('preserves root while stripping only non-semantic trailing slashes', () => {
    expect(normalizePickerPath('/', '/home/remote')).toBe('/');
    expect(normalizePickerPath('~/', '/home/remote')).toBe('/home/remote');
    expect(normalizePickerPath('/tmp/project/', '/home/remote')).toBe('/tmp/project');
    expect(normalizePickerPath('/tmp/project', '/home/remote')).toBe('/tmp/project');
  });
});
