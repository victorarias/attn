import { triggerShortcut } from '../../shortcuts/useShortcut';
import { isMacLikePlatform } from '../../shortcuts/platform';

export function installTerminalKeyHandler(sendToPty: (data: string) => void) {
  return (event: KeyboardEvent) => {
    if (
      event.type === 'keydown'
      && (event.key === 'Tab' || event.key === 'ISO_Left_Tab')
      && event.shiftKey
      && !event.ctrlKey
      && !event.metaKey
      && !event.altKey
    ) {
      sendToPty('\x1b[Z');
      return false;
    }
    if (
      event.type === 'keydown'
      && event.ctrlKey
      && !event.metaKey
      && !event.altKey
      && !event.shiftKey
      && event.key.toLowerCase() === 'v'
      && isMacLikePlatform()
    ) {
      // On macOS Ctrl+V is available for the agent image-paste trigger;
      // elsewhere it is the normal browser text-paste accelerator.
      sendToPty('\x16');
      return false;
    }
    const accel = isMacLikePlatform() ? event.metaKey : (event.metaKey || event.ctrlKey);
    if (event.type === 'keydown' && accel && !event.altKey) {
      if (!event.shiftKey) {
        const digitMatch = event.code.match(/^Digit([1-9])$/);
        const digit = digitMatch?.[1] ?? (/^[1-9]$/.test(event.key) ? event.key : null);
        if (digit) {
          return !triggerShortcut(`workspace.select${digit}` as Parameters<typeof triggerShortcut>[0]);
        }
      }
      if (!event.shiftKey && event.key.toLowerCase() === 't') {
        return !triggerShortcut('session.newWorkspace');
      }
      if (event.shiftKey && event.key.toLowerCase() === 'n') {
        return !triggerShortcut('session.newHorizontal');
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
        if (triggerShortcut('terminal.close')) {
          return false;
        }
        return !triggerShortcut('session.close');
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
