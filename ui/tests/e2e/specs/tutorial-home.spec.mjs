import { test, expect, ready, on } from '../harness.mjs';

for (const width of [1440, 920, 390]) {
    test(`tutorials form a collapsible column above the dashboard cards at ${width}px`, on('/'), async ({ page }) => {
        await page.setViewportSize({ width, height: 900 });
        await page.goto('/');
        await ready(page);
        const tutorials = page.locator('[data-abom-tutorial-checklist]');
        await expect(tutorials.locator('summary')).toHaveText('ABOM Tutorials');
        await expect(tutorials).toHaveAttribute('open', '');
        await expect(tutorials.locator('[data-abom-tutorial-title]')).toHaveCount(6);
        await expect(tutorials).not.toContainText('Walkthrough completion');
        const rows = await tutorials.locator('li').evaluateAll(items => items.map(el => {
            const r = el.getBoundingClientRect();
            return { x: r.x, top: r.top, bottom: r.bottom };
        }));
        rows.forEach((row, i) => {
            expect(row.x).toBe(rows[0].x);
            if (i) expect(row.top).toBeGreaterThanOrEqual(rows[i - 1].bottom);
        });
        const cards = page.locator('[data-abom-entry="findings"]');
        expect((await tutorials.boundingBox()).y + (await tutorials.boundingBox()).height)
            .toBeLessThanOrEqual((await cards.boundingBox()).y);
        await tutorials.locator('summary').click();
        await expect(tutorials.locator('ul')).toBeHidden();
        await expect(cards).toBeVisible();
        await tutorials.locator('summary').click();
        await expect(tutorials.locator('ul')).toBeVisible();
        const doc = await page.evaluate(() => ({ width: innerWidth, scroll: document.documentElement.scrollWidth }));
        expect(doc.scroll).toBeLessThanOrEqual(doc.width);
    });
}

test('titles describe and resume tutorials while icons have terse action tooltips', on('/', '/inventory'), async ({ page }) => {
    await page.goto('/');
    await ready(page);
    const title = page.locator('[data-abom-tutorial-title="orientation"]');
    await title.hover();
    await expect(page.locator('.mud-tooltip.mud-popover-open')).toContainText('Settings and coverage');
    const play = page.locator('[data-abom-tutorial-start="orientation"]');
    await expect(play).toHaveText('');
    await play.hover();
    await expect(page.locator('.mud-tooltip.mud-popover-open').filter({ hasText: 'Start this tutorial.' })).toBeVisible();
    await title.click();
    await expect(page.locator('[data-abom-tutorial-next]')).toBeEnabled();
    await page.locator('[data-abom-tutorial-next]').click();
    await expect(page.locator('.abom-tutorial-position').first()).toContainText('2 of');
    await page.locator('[data-abom-tutorial-exit]').click();
    await expect(play).toHaveAttribute('aria-label', /^Resume:/);
    await title.click();
    await expect(page.locator('.abom-tutorial-position').first()).toContainText('2 of');
    await page.keyboard.press('Escape');
});

test('an Action selection survives exiting and resuming from its title', on('/', '/inventory'), async ({ page, request }) => {
    const actions = await (await request.get('/api/actions?top=1')).json();
    test.skip(actions.total === 0, 'requires a recorded Action');
    await page.goto('/');
    await ready(page);
    await page.locator('[data-abom-tutorial-title="action-dependents"]').click();
    const action = page.locator('[data-abom-tutorial-active-action]').first();
    await expect(action).toBeVisible();
    await action.click();
    await expect(page.locator('.abom-tutorial-position').first()).toContainText('2 of');
    await expect(page).toHaveURL(/\/node\//);
    const target = page.url();
    await page.locator('[data-abom-tutorial-exit]').click();
    await page.locator('[data-abom-tutorial-title="action-dependents"]').click();
    await expect(page.locator('.abom-tutorial-position').first()).toContainText('2 of');
    await expect(page).toHaveURL(target);
    await page.keyboard.press('Escape');
});

for (const theme of ['light', 'dark']) {
    test(`the ${theme} spotlight dims the background without dimming its target`, on('/'), async ({ page }) => {
        await page.emulateMedia({ colorScheme: theme });
        await page.goto('/');
        await ready(page);
        await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
        await page.locator('[data-abom-tutorial-title="orientation"]').click();
        await expect(page.locator('[data-abom-tutorial-next]')).toBeEnabled();
        const target = page.locator('[data-abom-identity]');
        const color = () => target.evaluate(el => getComputedStyle(el).backgroundColor);
        const during = await color();
        const fill = await page.locator('.abom-tutorial-shade').evaluate(el => getComputedStyle(el).fill);
        const channels = fill.match(/[\d.]+/g).map(Number);
        expect(channels.slice(0, 3)).toEqual([0, 0, 0]);
        expect(channels[3]).toBeGreaterThanOrEqual(theme === 'dark' ? 0.8 : 0.4);
        await expect(page.locator('[data-abom-tutorial-toggle]')).toHaveCount(0);
        await page.locator('[data-abom-tutorial-exit]').click();
        await page.goto('/settings');
        await expect(target).toBeVisible();
        expect(await color()).toBe(during);
    });
}

test('Settings offers tutorials without removing the header discovery tracker', on('/settings'), async ({ page }) => {
    await page.addLocatorHandler(page.getByRole('button', { name: 'Not now', exact: true }),
        async button => { await button.click(); });
    await page.goto('/settings');
    await ready(page);
    await expect(page.locator('[data-abom-crawl]')).toBeVisible();
    await expect(page.locator('[data-abom-tutorial-title]')).toHaveCount(6);
    await page.locator('[data-abom-tutorial-title="new-rule"]').click();
    await expect(page.locator('.abom-tutorial-position').first()).toContainText('Request a new detection rule');
    await page.keyboard.press('Escape');
    await expect(page).toHaveURL(/\/settings$/);
});
