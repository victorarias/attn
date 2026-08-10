import { expect, test, type Page } from '@playwright/test';

async function openHarness(page: Page) {
  await page.goto('/test-harness/?component=MermaidDiagram');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
}

test.describe('large Mermaid diagram reader', () => {
  test('keeps small diagrams fitted and makes both real wide diagrams readable', async ({ page }) => {
    await openHarness(page);

    const diagrams = page.locator('.markdown-mermaid');
    await expect(diagrams).toHaveCount(3);
    await expect(diagrams.nth(0)).not.toHaveClass(/markdown-mermaid--oversized/);
    await expect(diagrams.nth(1)).toHaveClass(/markdown-mermaid--oversized/);
    await expect(diagrams.nth(2)).toHaveClass(/markdown-mermaid--oversized/);
    await expect(page.getByRole('button', { name: 'Focus diagram' })).toHaveCount(2);

    for (const index of [1, 2]) {
      const receipt = await diagrams.nth(index).evaluate((viewport) => {
        const svg = viewport.querySelector('svg')!;
        const viewBoxWidth = svg.viewBox.baseVal.width;
        return {
          viewBoxWidth,
          renderedWidth: svg.getBoundingClientRect().width,
          clientWidth: viewport.clientWidth,
          scrollWidth: viewport.scrollWidth,
        };
      });
      expect(receipt.renderedWidth).toBeCloseTo(receipt.viewBoxWidth, 0);
      expect(receipt.scrollWidth).toBeGreaterThan(receipt.clientWidth);
    }
  });

  test('opens one SVG in focus, fits and zooms it, then returns to the exact viewport', async ({ page }) => {
    await openHarness(page);

    const viewport = page.locator('.markdown-mermaid--oversized').first();
    await viewport.focus();
    await page.keyboard.press('ArrowRight');
    await expect.poll(() => viewport.evaluate((node) => node.scrollLeft)).toBeGreaterThan(0);
    await page.keyboard.press('Enter');

    const dialog = page.getByRole('dialog', { name: 'Mermaid diagram' });
    await expect(dialog).toBeVisible();
    await expect(page.locator('.markdown-mermaid svg')).toHaveCount(2);
    await expect(page.locator('.md-diagram-focus-stage svg')).toHaveCount(1);

    const focusedSvg = page.locator('.md-diagram-focus-stage svg');
    const markerReceipt = await focusedSvg.evaluate((svg) => {
      const marked = svg.querySelector<SVGElement>('[marker-end]');
      const markerEnd = marked?.getAttribute('marker-end') ?? '';
      const id = markerEnd.match(/#([^)'\"]+)/)?.[1];
      return { markerEnd, markerExists: Boolean(id && svg.querySelector(`#${CSS.escape(id)}`)) };
    });
    expect(markerReceipt.markerEnd).toContain('url(');
    expect(markerReceipt.markerExists).toBe(true);

    await page.getByRole('button', { name: 'Fit' }).click();
    await expect.poll(async () => {
      const text = await page.locator('.md-diagram-focus-readout').textContent();
      return Number(text?.replace('%', ''));
    }).toBeLessThan(100);

    await page.keyboard.press('1');
    await expect(page.locator('.md-diagram-focus-readout')).toHaveText('100%');
    await page.keyboard.press('+');
    await expect(page.locator('.md-diagram-focus-readout')).toHaveText('110%');
    await page.keyboard.press('Escape');

    await expect(dialog).toHaveCount(0);
    await expect(viewport).toBeFocused();
    await expect(page.locator('.markdown-mermaid svg')).toHaveCount(3);
  });
});
