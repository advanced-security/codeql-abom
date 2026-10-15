import { test, expect, ready, on } from '../harness.mjs';

// `make restart` replaces the server, not the page: a tab opened before it keeps running the build
// it booted from, and keeps working, so nothing on it said why the Audit log tab the server now
// served was nowhere to be seen. The page compares itself with what the server serves now.
test.describe('a page loaded before the server changed', () => {
    test('says it is running an earlier build, and reloads into the current one', on('/codeql'), async ({ page }) => {
        await page.goto('/codeql');
        await ready(page);
        const notice = page.locator('#abom-stale-build');
        expect(await page.evaluate(() => window.abomBuild.check()), 'the build served is the one loaded').toBe(false);
        await expect(notice).toBeHidden();

        // The server now answers with an entry point naming other framework files.
        const entry = (url) => url.pathname === '/';
        await page.route(entry, async (route) => {
            const served = await route.fetch();
            const html = (await served.text()).replace(/dotnet\.([0-9a-z]+)\.js/g, 'dotnet.$1z.js');
            await route.fulfill({ response: served, body: html });
        });
        expect(await page.evaluate(() => window.abomBuild.check()), 'a newer build is served').toBe(true);
        await expect(notice).toBeVisible();
        await expect(notice).toContainText('earlier build');

        await page.unroute(entry);
        await notice.getByRole('button', { name: 'Reload' }).click();
        await ready(page);
        await expect(notice).toBeHidden();
        expect(await page.evaluate(() => window.abomBuild.check()), 'the reloaded page is current').toBe(false);
    });
});
