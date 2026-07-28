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

  it('persists and restores the spotlight style', () => {
    persistWorkspaceSelectionStyle('spotlight');

    expect(localStorage.getItem(WORKSPACE_SELECTION_STYLE_STORAGE_KEY)).toBe('spotlight');
    expect(readWorkspaceSelectionStyle()).toBe('spotlight');
  });
});
