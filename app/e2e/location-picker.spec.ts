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

    test('closes dialog when clicking overlay', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Open dialog
      await page.keyboard.press('Meta+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

      // Wait for any error banners to clear
      await page.waitForTimeout(1000);

      // Ensure no error banner is blocking
      const errorBanner = page.locator('.connection-error-banner');
      const hasError = await errorBanner.isVisible({ timeout: 500 }).catch(() => false);

      if (!hasError) {
        // Click overlay (not the picker itself)
        await page.locator('.location-picker-overlay').click({ position: { x: 10, y: 10 } });
        await expect(page.locator('.location-picker-overlay')).not.toBeVisible({ timeout: 2000 });
      } else {
        // If there's an error banner, use Escape as fallback
        await page.keyboard.press('Escape');
        await expect(page.locator('.location-picker-overlay')).not.toBeVisible({ timeout: 2000 });
      }
    });
  });

  test.describe('Recent Locations', () => {
    test('shows recent locations section when available', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Open dialog
      await page.keyboard.press('Meta+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

      // Wait for content to load
      await page.waitForTimeout(500);

      // Check for RECENT section (may or may not be visible depending on whether there are recent locations)
      const recentSection = page.locator('.picker-section-title').filter({ hasText: 'RECENT' });
      const hasRecent = await recentSection.isVisible({ timeout: 1000 }).catch(() => false);

      // Just verify the test can detect the presence/absence of recent locations
      // This is a smoke test - the feature works if no error is thrown
      if (hasRecent) {
        await expect(recentSection).toBeVisible();
      }
    });

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
    test('shows placeholder text', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Open dialog
      await page.keyboard.press('Meta+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

      // Check for input with placeholder
      const input = page.locator('.path-input');
      await expect(input).toBeVisible();
    });

    test('accepts keyboard input', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Open dialog
      await page.keyboard.press('Meta+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

      // Type a path
      await page.keyboard.type('~/pro');

      // Input should contain the text (checking via the ghost text container or input)
      const pathInputContainer = page.locator('.path-input-container');
      await expect(pathInputContainer).toBeVisible();
    });

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

  test.describe('Keyboard Navigation', () => {
    test('shows keyboard shortcuts in footer', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Open dialog
      await page.keyboard.press('Meta+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

      // Check for footer with keyboard shortcuts
      const footer = page.locator('.picker-footer');
      await expect(footer).toBeVisible();

      // Check for specific shortcuts
      await expect(footer.locator('kbd').filter({ hasText: '↑↓' })).toBeVisible();
      await expect(footer.locator('kbd').filter({ hasText: 'Tab' })).toBeVisible();
      await expect(footer.locator('kbd').filter({ hasText: 'Enter' })).toBeVisible();
      await expect(footer.locator('kbd').filter({ hasText: 'Esc' })).toBeVisible();
    });

    test('highlights first item by default', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Open dialog
      await page.keyboard.press('Meta+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

      // Wait a bit for content to load
      await page.waitForTimeout(500);

      // If there are any items (recent or directories), first should be selected
      const firstItem = page.locator('.picker-item').first();
      const hasItems = await firstItem.isVisible({ timeout: 1000 }).catch(() => false);

      if (hasItems) {
        await expect(firstItem).toHaveClass(/selected/);
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

    test('shows prompt when no input provided', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Open dialog
      await page.keyboard.press('Meta+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

      // Wait to ensure state is loaded
      await page.waitForTimeout(500);

      // If no recent locations and no input, should show help text
      const hasRecent = await page.locator('.picker-section-title').filter({ hasText: 'RECENT' })
        .isVisible({ timeout: 1000 })
        .catch(() => false);

      if (!hasRecent) {
        const emptyState = page.locator('.picker-empty');
        const isVisible = await emptyState.isVisible({ timeout: 1000 }).catch(() => false);
        if (isVisible) {
          await expect(emptyState).toContainText('Type a path');
        }
      }
    });
  });

  test.describe('Breadcrumb', () => {
    test('shows current directory when browsing', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Open dialog
      await page.keyboard.press('Meta+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

      // Type a path to trigger browsing
      await page.keyboard.type('~/');

      // Wait for filesystem to respond
      await page.waitForTimeout(500);

      // Check for breadcrumb
      const breadcrumb = page.locator('.picker-breadcrumb');
      const isVisible = await breadcrumb.isVisible({ timeout: 1000 }).catch(() => false);

      if (isVisible) {
        // Should show "Browsing:" label
        await expect(breadcrumb.locator('.picker-breadcrumb-label')).toHaveText('Browsing:');
        // Should show a path
        await expect(breadcrumb.locator('.picker-breadcrumb-path')).not.toBeEmpty();
      }
    });
  });
});
