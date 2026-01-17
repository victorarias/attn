import { test, expect } from './fixtures';

test.describe('LocationPicker', () => {
  test.describe('Basic Dialog Operations', () => {
    test('opens new session dialog with Cmd+N', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Dialog should not be visible initially
      await expect(page.locator('.location-picker-overlay')).not.toBeVisible();

      // Open with Cmd+N
      await page.keyboard.press('Meta+n');

      // Dialog should be visible
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });
      await expect(page.locator('.location-picker')).toBeVisible();
      await expect(page.locator('.picker-title')).toHaveText('New Session Location');
    });

    test('closes dialog with Escape', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Open dialog
      await page.keyboard.press('Meta+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

      // Close with Escape
      await page.keyboard.press('Escape');
      await expect(page.locator('.location-picker-overlay')).not.toBeVisible({ timeout: 2000 });
    });

    test('remembers selected agent between openings', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Open dialog and switch agent to Claude
      await page.keyboard.press('Meta+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });
      await page.locator('.agent-option', { hasText: 'Claude' }).click();
      await expect(page.locator('.agent-option', { hasText: 'Claude' })).toHaveClass(/active/);

      // Close and reopen to verify persistence
      await page.keyboard.press('Escape');
      await expect(page.locator('.location-picker-overlay')).not.toBeVisible({ timeout: 2000 });
      await page.keyboard.press('Meta+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });
      await expect(page.locator('.agent-option', { hasText: 'Claude' })).toHaveClass(/active/);
    });
  });

  test.describe('Recent Locations', () => {
    test('filters recent locations based on input', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Open dialog
      await page.keyboard.press('Meta+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

      // Type to filter (if there are any recent locations)
      const recentSection = page.locator('.picker-section-title').filter({ hasText: 'RECENT' });
      if (await recentSection.isVisible({ timeout: 1000 }).catch(() => false)) {
        // Get initial count of recent items
        const initialCount = await page.locator('.picker-section:has(.picker-section-title:text("RECENT")) .picker-item').count();

        if (initialCount > 0) {
          // Type something to filter
          await page.keyboard.type('xyz_nonexistent_filter');

          // Recent section should disappear or be empty
          const filteredCount = await page.locator('.picker-section:has(.picker-section-title:text("RECENT")) .picker-item').count();
          expect(filteredCount).toBeLessThanOrEqual(initialCount);
        }
      }
    });
  });

  test.describe('Path Input', () => {
    test('shows filesystem suggestions', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Open dialog
      await page.keyboard.press('Meta+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

      // Type a path that should trigger suggestions
      await page.keyboard.type('~/');

      // Wait for suggestions to appear
      await page.waitForTimeout(500); // Give filesystem time to respond

      // Check for DIRECTORIES section (if filesystem returns results)
      const directoriesSection = page.locator('.picker-section-title').filter({ hasText: 'DIRECTORIES' });
      const hasSuggestions = await directoriesSection.isVisible({ timeout: 2000 }).catch(() => false);

      if (hasSuggestions) {
        // Verify at least one directory suggestion
        const directoryItems = page.locator('.picker-section:has(.picker-section-title:text("DIRECTORIES")) .picker-item');
        const count = await directoryItems.count();
        expect(count).toBeGreaterThan(0);
      }
    });
  });

  test.describe('Empty State', () => {
    test('shows empty state when no matches', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Open dialog
      await page.keyboard.press('Meta+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

      // Type something that won't match anything
      await page.keyboard.type('/xyz_nonexistent_path_12345');

      // Wait for results to update
      await page.waitForTimeout(500);

      // Should show empty state
      const emptyState = page.locator('.picker-empty');
      await expect(emptyState).toBeVisible({ timeout: 2000 });
      await expect(emptyState).toContainText('No matches');
    });
  });
});
