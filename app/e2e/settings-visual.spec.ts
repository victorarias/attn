import * as fs from 'fs';
import { test, expect } from './fixtures';

const viewports = [
  { name: 'desktop', width: 1440, height: 900 },
  { name: 'tablet', width: 1024, height: 820 },
  { name: 'mobile', width: 430, height: 860 },
] as const;

const sections = ['general', 'connectivity', 'plugins', 'agents', 'review', 'hygiene'] as const;

test.describe('Settings visual harness', () => {
  for (const viewport of viewports) {
    test(`captures settings workbench at ${viewport.name} size`, async ({ page, startDaemonWithPRs }, testInfo) => {
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await startDaemonWithPRs();
      await page.goto('/');

      await expect(page.locator('.dashboard')).toBeVisible({ timeout: 5000 });
      await page.getByTestId('settings-button').click();

      const modal = page.getByTestId('settings-modal');
      await expect(modal).toBeVisible({ timeout: 2000 });

      const modalBox = await modal.boundingBox();
      expect(modalBox?.width ?? 0).toBeGreaterThan(Math.min(300, viewport.width - 80));
      expect(modalBox?.height ?? 0).toBeGreaterThan(Math.min(320, viewport.height - 120));

      const body = page.getByTestId('settings-body');
      for (const section of sections) {
        await modal.getByTestId(`settings-nav-${section}`).click();
        await body.evaluate((node) => {
          node.scrollTop = 0;
        });
        await page.waitForTimeout(80);

        const sectionPath = testInfo.outputPath(`settings-modal-${viewport.name}-${section}.png`);
        await modal.screenshot({ path: sectionPath });
        expect(fs.existsSync(sectionPath)).toBe(true);
      }

      await modal.getByTestId('settings-nav-agents').click();
      await body.evaluate((node) => {
        node.scrollTop = node.scrollHeight;
      });
      await page.waitForTimeout(80);

      const bottomPath = testInfo.outputPath(`settings-modal-${viewport.name}-agents-bottom.png`);
      await modal.screenshot({ path: bottomPath });
      expect(fs.existsSync(bottomPath)).toBe(true);
    });
  }
});
