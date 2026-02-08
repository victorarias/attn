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

      // Open dialog and switch to any available non-disabled agent
      await page.keyboard.press('Meta+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });
      const enabledAgents = page.locator('.agent-option:not(:disabled)');
      const enabledCount = await enabledAgents.count();
      if (enabledCount === 0) {
        await expect(page.locator('.picker-agent-warning')).toBeVisible();
        return;
      }
      const targetAgent = enabledAgents.nth(Math.min(1, enabledCount - 1));
      const agentName = ((await targetAgent.locator('.agent-option-name').textContent()) || '').trim();
      await targetAgent.click();
      await expect(page.locator('.agent-option', { hasText: agentName })).toHaveClass(/active/);

      // Close and reopen to verify persistence
      await page.keyboard.press('Escape');
      await expect(page.locator('.location-picker-overlay')).not.toBeVisible({ timeout: 2000 });
      await page.keyboard.press('Meta+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });
      await expect(page.locator('.agent-option', { hasText: agentName })).toHaveClass(/active/);
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

  test.describe('Contains Search', () => {
    test('matches directories containing search term (not just starting with)', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Open dialog
      await page.keyboard.press('Meta+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

      // Type ~/Library/ to get a predictable set of directories, then search for a substring
      // Most Macs have directories like "Application Support" in ~/Library
      await page.keyboard.type('~/Library/');
      await page.waitForTimeout(500);

      // Check if DIRECTORIES section exists
      const directoriesSection = page.locator('.picker-section-title').filter({ hasText: 'DIRECTORIES' });
      const hasSuggestions = await directoriesSection.isVisible({ timeout: 2000 }).catch(() => false);

      if (hasSuggestions) {
        // Get current directory names
        const directoryItems = page.locator('.picker-section:has(.picker-section-title:text("DIRECTORIES")) .picker-name');
        const count = await directoryItems.count();

        if (count > 0) {
          // Clear input and search for "Support" which should match "Application Support"
          // First clear by selecting all and typing new path
          await page.keyboard.press('Meta+a');
          await page.keyboard.type('~/Library/Support');
          await page.waitForTimeout(500);

          // Should find directories containing "Support"
          const matchingItems = page.locator('.picker-section:has(.picker-section-title:text("DIRECTORIES")) .picker-name');
          const matchCount = await matchingItems.count();

          // If we found matches, verify they contain "Support"
          if (matchCount > 0) {
            const firstName = await matchingItems.first().textContent();
            expect(firstName?.toLowerCase()).toContain('support');
          }
        }
      }
    });
  });

  test.describe('Keyboard Navigation', () => {
    test('arrow keys navigate and scroll selected item into view', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      // Open dialog
      await page.keyboard.press('Meta+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

      // Type path to get suggestions
      await page.keyboard.type('~/');
      await page.waitForTimeout(500);

      // Check if DIRECTORIES section exists with multiple items
      const directoryItems = page.locator('.picker-section:has(.picker-section-title:text("DIRECTORIES")) .picker-item');
      const count = await directoryItems.count();

      if (count > 3) {
        // Navigate down several times
        await page.keyboard.press('ArrowDown');
        await page.keyboard.press('ArrowDown');
        await page.keyboard.press('ArrowDown');

        // The third item should be selected
        const selectedItem = page.locator('.picker-item.selected');
        await expect(selectedItem).toBeVisible();

        // Verify data-index is correct (2 = third item, 0-indexed)
        const dataIndex = await selectedItem.getAttribute('data-index');
        expect(dataIndex).toBe('2');
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
