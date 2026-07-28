import { beforeEach, describe, expect, it } from 'vitest';
import {
  persistWorkspaceSelectionStyle,
  readWorkspaceSelectionStyle,
  WORKSPACE_SELECTION_STYLE_STORAGE_KEY,
} from './workspaceSelectionStyle';

describe('workspace selection style preference', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('defaults missing and unknown values to the edge rail', () => {
    expect(readWorkspaceSelectionStyle()).toBe('rail');

    localStorage.setItem(WORKSPACE_SELECTION_STYLE_STORAGE_KEY, 'unknown');
    expect(readWorkspaceSelectionStyle()).toBe('rail');
  });

  it.each(['dim', 'spotlight'] as const)('persists and restores the %s style', (style) => {
    persistWorkspaceSelectionStyle(style);

    expect(localStorage.getItem(WORKSPACE_SELECTION_STYLE_STORAGE_KEY)).toBe(style);
    expect(readWorkspaceSelectionStyle()).toBe(style);
  });
});
