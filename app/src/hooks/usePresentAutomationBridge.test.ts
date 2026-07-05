import { describe, expect, it } from 'vitest';
import { isPresentWindowAction } from './usePresentAutomationBridge';

describe('isPresentWindowAction', () => {
  it('routes present_window_* actions to the present window bridge', () => {
    expect(isPresentWindowAction('present_window_submit')).toBe(true);
    expect(isPresentWindowAction('present_window_is_visible')).toBe(true);
  });

  it('routes everything else to the main window bridge', () => {
    expect(isPresentWindowAction('present_click_chip')).toBe(false);
    expect(isPresentWindowAction('get_state')).toBe(false);
    expect(isPresentWindowAction('create_session')).toBe(false);
  });
});
