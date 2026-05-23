import { afterEach, describe, expect, it } from 'vitest';
import { waitFor } from '../test/utils';
import { installVerbatimTextEntryGuard } from './verbatimTextEntry';

describe('installVerbatimTextEntryGuard', () => {
  afterEach(() => {
    document.body.innerHTML = '';
  });

  it('disables browser rewriting on existing editable controls', () => {
    document.body.innerHTML = '<input type="search"><textarea></textarea><div contenteditable="true"></div>';

    const dispose = installVerbatimTextEntryGuard(document.body);

    for (const element of document.body.querySelectorAll('input, textarea, [contenteditable]')) {
      expect(element).toHaveAttribute('autocorrect', 'off');
      expect(element).toHaveAttribute('autocapitalize', 'none');
      expect(element).toHaveAttribute('spellcheck', 'false');
    }

    dispose();
  });

  it('disables browser rewriting on controls created by runtime widgets', async () => {
    const dispose = installVerbatimTextEntryGuard(document.body);
    const terminalTextarea = document.createElement('textarea');
    terminalTextarea.className = 'xterm-helper-textarea';
    terminalTextarea.setAttribute('autocorrect', 'on');
    document.body.appendChild(terminalTextarea);

    await waitFor(() => {
      expect(terminalTextarea).toHaveAttribute('autocorrect', 'off');
      expect(terminalTextarea).toHaveAttribute('autocapitalize', 'none');
      expect(terminalTextarea).toHaveAttribute('spellcheck', 'false');
    });

    dispose();
  });
});
