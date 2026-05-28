import { triggerShortcut } from '../../shortcuts/useShortcut';
import { isMacLikePlatform } from '../../shortcuts/platform';

export function installTerminalKeyHandler(sendToPty: (data: string) => void) {
  return (event: KeyboardEvent) => {
    const accel = isMacLikePlatform() ? event.metaKey : (event.metaKey || event.ctrlKey);
    if (event.type === 'keydown' && accel && !event.altKey) {
      if (!event.shiftKey && event.key.toLowerCase() === 't') {
        return !triggerShortcut('terminal.new');
      }
      if (!event.shiftKey && event.key.toLowerCase() === 'd') {
        return !triggerShortcut('terminal.splitVertical');
      }
      if (event.shiftKey && event.key.toLowerCase() === 'd') {
        return !triggerShortcut('terminal.splitHorizontal');
      }
      if (event.shiftKey && event.key.toLowerCase() === 'z') {
        return !triggerShortcut('terminal.toggleZoom');
      }
      if (event.shiftKey && event.key === 'Enter') {
        return !triggerShortcut('terminal.toggleMaximize');
      }
      if (!event.shiftKey && event.key.toLowerCase() === 'w') {
        triggerShortcut('terminal.close');
        return false;
      }
    }
    if (event.key === 'Enter' && event.shiftKey && !event.ctrlKey && !event.altKey) {
      if (event.type === 'keydown') {
        sendToPty('\n');
      }
      return false;
    }
    return true;
  };
}
