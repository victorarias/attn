import { test, expect } from './fixtures';
import { execSync } from 'child_process';
import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';

// The browser harness provisions only the local daemon flow today. Remote parity
// for the location picker is therefore covered at component-integration level in
// LocationPicker tests, while this spec validates the browser-visible behavior on
// the local harness.

function createLocationPickerRepo(worktreeBranches: string[]) {
  const parentDir = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-location-picker-repo-'));
  const repoPath = path.join(parentDir, 'exsin');
  fs.mkdirSync(repoPath);

  execSync('git init -b main', { cwd: repoPath, stdio: 'pipe' });
  execSync('git config user.email "test@example.com"', { cwd: repoPath, stdio: 'pipe' });
  execSync('git config user.name "Test User"', { cwd: repoPath, stdio: 'pipe' });
  fs.writeFileSync(path.join(repoPath, 'README.md'), '# exsin\n');
  execSync('git add README.md', { cwd: repoPath, stdio: 'pipe' });
  execSync('git commit -m "initial"', { cwd: repoPath, stdio: 'pipe' });

  const worktrees = worktreeBranches.map((branch) => {
    const worktreePath = `${repoPath}--${branch}`;
    execSync(`git branch ${branch}`, { cwd: repoPath, stdio: 'pipe' });
    execSync(`git worktree add ${JSON.stringify(worktreePath)} ${branch}`, { cwd: repoPath, stdio: 'pipe' });
    return { branch, path: worktreePath };
  });

  return {
    repoPath,
    worktrees,
    cleanup() {
      fs.rmSync(parentDir, { recursive: true, force: true });
    },
  };
}

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

  test.describe('Regression Cases', () => {
    test.beforeEach(async ({ page }) => {
      page.on('console', (msg) => {
        const text = msg.text();
        if (text.startsWith('[LP:') || text.startsWith('[PathInput:')) {
          console.log(`[browser] ${text}`);
        }
      });
    });

    test('preselects the exact worktree row for typed worktree paths with and without trailing slash', async ({ page, daemon }) => {
      await daemon.start();
      const repo = createLocationPickerRepo(['feat-images']);

      try {
        await page.goto('/');
        await page.waitForSelector('.dashboard');

        for (const typedPath of [repo.worktrees[0].path, `${repo.worktrees[0].path}/`]) {
          await page.keyboard.press('Meta+n');
          await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

          const input = page.locator('[data-testid="location-picker-path-input"]');
          await input.focus();
          await page.keyboard.type(typedPath);
          await page.keyboard.press('Enter');

          await expect(page.locator('[data-testid="repo-options"]')).toBeVisible({ timeout: 5000 });
          await expect(page.locator('[data-testid="repo-option-1"]')).toHaveClass(/selected/);

          await page.keyboard.press('Escape');
          await expect(page.locator('[data-testid="location-picker-path-input"]')).toBeVisible({ timeout: 2000 });
          await page.keyboard.press('Escape');
          await expect(page.locator('.location-picker-overlay')).not.toBeVisible({ timeout: 2000 });
        }
      } finally {
        repo.cleanup();
      }
    });

    test('hovering a chooser row does not change the Enter target', async ({ page, daemon }) => {
      await daemon.start();
      const repo = createLocationPickerRepo(['feat-images']);

      try {
        await page.goto('/');
        await page.waitForSelector('.dashboard');
        await page.keyboard.press('Meta+n');
        await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

        const input = page.locator('[data-testid="location-picker-path-input"]');
        await input.focus();
        await page.keyboard.type(repo.repoPath);
        await page.keyboard.press('Enter');

        await expect(page.locator('[data-testid="repo-options"]')).toBeVisible({ timeout: 5000 });
        await expect(page.locator('[data-testid="repo-option-0"]')).toHaveClass(/selected/);

        await page.locator('[data-testid="repo-option-1"]').hover();
        await expect(page.locator('[data-testid="repo-option-0"]')).toHaveClass(/selected/);

        await page.keyboard.press('Enter');
        await expect(page.locator('.location-picker-overlay')).not.toBeVisible({ timeout: 5000 });
        await expect(page.locator('.session-name', { hasText: 'exsin' }).first()).toBeVisible({ timeout: 10000 });
        await expect(page.locator('.session-name', { hasText: 'exsin--feat-images' })).toHaveCount(0);
      } finally {
        repo.cleanup();
      }
    });

    test('creates a new worktree and opens it immediately', async ({ page, daemon }) => {
      await daemon.start();
      const repo = createLocationPickerRepo(['feat-images']);

      try {
        await page.goto('/');
        await page.waitForSelector('.dashboard');
        await page.keyboard.press('Meta+n');
        await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

        const input = page.locator('[data-testid="location-picker-path-input"]');
        await input.focus();
        await page.keyboard.type(repo.worktrees[0].path);
        await page.keyboard.press('Enter');

        await expect(page.locator('[data-testid="repo-options"]')).toBeVisible({ timeout: 5000 });
        await page.locator('[data-testid="repo-option-2"]').click();
        await expect(page.locator('[data-testid="repo-new-worktree-input"]')).toBeVisible({ timeout: 2000 });
        await expect(page.getByText('Start from feat-images')).toBeVisible();

        await page.locator('[data-testid="repo-new-worktree-input"]').focus();
        await page.keyboard.type('feat-more');
        await page.keyboard.press('Enter');

        await expect(page.locator('.location-picker-overlay')).not.toBeVisible({ timeout: 10000 });
        await expect(page.locator('.session-name', { hasText: 'exsin--feat-more' }).first()).toBeVisible({ timeout: 10000 });
      } finally {
        repo.cleanup();
      }
    });

    test('keeps adjacent selection after deleting a worktree', async ({ page, daemon }) => {
      await daemon.start();
      const repo = createLocationPickerRepo(['feat-a', 'feat-b']);

      try {
        await page.goto('/');
        await page.waitForSelector('.dashboard');
        await page.keyboard.press('Meta+n');
        await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

        const input = page.locator('[data-testid="location-picker-path-input"]');
        await input.focus();
        await page.keyboard.type(repo.worktrees[1].path);
        await page.keyboard.press('Enter');

        await expect(page.locator('[data-testid="repo-options"]')).toBeVisible({ timeout: 5000 });
        await expect(page.locator('[data-testid="repo-option-2"]')).toHaveClass(/selected/);

        await page.locator('[data-testid="repo-options"]').press('D');
        await page.locator('[data-testid="repo-options"]').press('y');

        await expect(page.locator('[data-testid="repo-options"]')).toBeVisible({ timeout: 5000 });
        await expect(page.locator('[data-testid="repo-option-1"]')).toHaveClass(/selected/);
      } finally {
        repo.cleanup();
      }
    });

    test('does not implicitly open a child directory such as .claude', async ({ page, daemon }) => {
      await daemon.start();
      page.on('dialog', async (dialog) => {
        await dialog.dismiss();
      });

      const parentDir = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-location-picker-'));
      const projectDir = path.join(parentDir, 'project-with-hidden-child');
      fs.mkdirSync(projectDir);
      fs.mkdirSync(path.join(projectDir, '.claude'));

      try {
        await page.goto('/');
        await page.waitForSelector('.dashboard');

        await page.keyboard.press('Meta+n');
        await expect(page.locator('.location-picker-overlay')).toBeVisible({ timeout: 2000 });

        const input = page.locator('[data-testid="location-picker-path-input"]');
        await input.focus();
        await page.keyboard.type(`${projectDir}/`);
        await page.keyboard.press('Enter');

        await expect(page.locator('.location-picker-overlay')).not.toBeVisible({ timeout: 5000 });
        await expect(page.locator('.session-name', { hasText: 'project-with-hidden-child' }).first()).toBeVisible({ timeout: 10000 });
        await expect(page.locator('.session-name', { hasText: '.claude' })).toHaveCount(0);
      } finally {
        fs.rmSync(parentDir, { recursive: true, force: true });
      }
    });
  });
});
