import { describe, expect, it } from 'vitest';
import { terminalStyledSelectionToMarkdown } from './terminalMarkdown';

describe('terminalStyledSelectionToMarkdown', () => {
  it('preserves semantic styling in a selected terminal range', () => {
    expect(terminalStyledSelectionToMarkdown([{
      runs: [
        { text: 'bold', bold: true, italic: false, strikethrough: false, underline: false, colored: false },
        { text: ' ', bold: false, italic: false, strikethrough: false, underline: false, colored: false },
        { text: 'italic', bold: false, italic: true, strikethrough: false, underline: false, colored: false },
      ],
    }])).toBe('**bold** *italic*');
  });

  it('uses inline code only when colored text is embedded in prose', () => {
    expect(terminalStyledSelectionToMarkdown([{
      runs: [
        { text: 'Use ', bold: false, italic: false, strikethrough: false, underline: false, colored: false },
        { text: 'attn', bold: false, italic: false, strikethrough: false, underline: false, colored: true },
        { text: ' here', bold: false, italic: false, strikethrough: false, underline: false, colored: false },
      ],
    }])).toBe('Use `attn` here');
  });

  it('joins soft-wrapped styled rows before cleaning whitespace', () => {
    expect(terminalStyledSelectionToMarkdown([
      { runs: [{ text: 'long ', bold: false, italic: false, strikethrough: false, underline: false, colored: false }] },
      { wrapped: true, runs: [{ text: 'line', bold: true, italic: false, strikethrough: false, underline: false, colored: false }] },
    ])).toBe('long **line**');
  });
});
