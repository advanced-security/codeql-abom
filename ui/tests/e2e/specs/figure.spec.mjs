import { test, expect, ready, on } from '../harness.mjs';

// A details page draws what runs its node and what it runs. bUnit has no CSS engine and no SVG
// layout, so where the figure sits, how it folds, how its header lines up and how it prints are
// held here, on whatever the shared data set has.

async function firstAction(request, baseURL) {
    const { items } = await (await request.get(`${baseURL}/api/actions?top=1&offset=0`)).json();
    return items?.[0]?.nodeKey;
}

async function drawn(page) {
    await expect(page.locator('[data-abom-figure-canvas] svg.abom-fig')).toBeVisible({ timeout: 30_000 });
}

test.describe('the figure on a details page', () => {
    test.beforeEach(async ({ page }) => {
        await page.addInitScript(() => localStorage.removeItem('abom.figurePlacement'));
    });

    // Open on arrival, and every node in it is a link a reader can name.
    test('is drawn on arrival with every node a named link', on('/node'), async ({ page, request, baseURL }) => {
        const key = await firstAction(request, baseURL);
        test.skip(!key, 'no action on this corpus');
        await page.setViewportSize({ width: 1280, height: 900 });
        await page.goto(`/node/${encodeURIComponent(key)}`);
        await ready(page);
        await drawn(page);

        const links = page.locator('svg.abom-fig a');
        expect(await links.count()).toBeGreaterThan(0);
        for (const name of await links.evaluateAll((as) => as.map((a) => a.getAttribute('aria-label') || ''))) {
            expect(name.trim(), 'every node link has an accessible name').not.toBe('');
        }
        await expect(page.locator('[data-abom-figure-toggle]')).toHaveAttribute('aria-expanded', 'true');
    });

    // Above the tables it folds up; beside them it folds sideways into a rail whose text reads
    // across, and the tables take the width it gave back.
    test('folds the way it sits', on('/node'), async ({ page, request, baseURL }) => {
        test.slow();
        const key = await firstAction(request, baseURL);
        test.skip(!key, 'no action on this corpus');
        await page.setViewportSize({ width: 2100, height: 1000 });
        await page.goto(`/node/${encodeURIComponent(key)}`);
        await ready(page);
        await drawn(page);
        await expect(page.locator('[data-abom-node-aside]'), 'a wide page puts it beside the tables').toBeVisible();

        const wide = await page.locator('.abom-node-main').evaluate((m) => m.getBoundingClientRect().width);
        await page.locator('[data-abom-figure-toggle]').click();
        const rail = page.locator('[data-abom-figure-rail]');
        await expect(rail).toBeVisible();
        await expect.poll(() => page.locator('.abom-node-main').evaluate((m) => m.getBoundingClientRect().width),
            'the tables take the width the figure gave back').toBeGreaterThan(wide + 300);
        // The column narrows over a short transition, so the rail's width is read once it settles.
        await expect.poll(async () => (await rail.boundingBox()).width, 'the rail is a narrow strip')
            .toBeLessThanOrEqual(96);
        const turned = await rail.evaluate((r) => [...r.querySelectorAll('*')]
            .some((e) => getComputedStyle(e).writingMode !== 'horizontal-tb'));
        expect(turned, 'no text in the rail is turned on its side').toBe(false);

        await rail.locator('[data-abom-figure-toggle]').click();
        await drawn(page);
        await page.locator('[data-abom-figure-placement]').click();
        await expect(page.locator('[data-abom-node-layout="top"] [data-abom-figure]')).toBeVisible();
        await page.locator('[data-abom-figure-toggle]').click();
        await expect(page.locator('[data-abom-figure-canvas]')).toHaveCount(0);
        await expect(page.locator('[data-abom-figure-summary]')).toContainText('drawn');
    });

    // The title, its chips, and each link's icon and words share one center line: the icons sat
    // 4.9 px above their words, and the words 4.7 px below the title.
    test('lines its header up on one center', on('/node'), async ({ page, request, baseURL }) => {
        const key = await firstAction(request, baseURL);
        test.skip(!key, 'no action on this corpus');
        await page.setViewportSize({ width: 1440, height: 900 });
        await page.goto(`/node/${encodeURIComponent(key)}`);
        await ready(page);
        const centers = await page.locator('.abom-node-head').evaluate((head) => {
            const middle = (r) => r.top + r.height / 2;
            const words = (el) => {
                const walk = document.createTreeWalker(el, NodeFilter.SHOW_TEXT,
                    { acceptNode: (n) => (n.textContent.trim() ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT) });
                const text = walk.nextNode();
                const range = document.createRange();
                range.selectNodeContents(text);
                return middle(range.getBoundingClientRect());
            };
            const out = [words(head.querySelector('h1'))];
            head.querySelectorAll('.mud-chip').forEach((c) => out.push(words(c)));
            head.querySelectorAll('a.abom-icon-link').forEach((a) => {
                out.push(middle(a.querySelector('svg').getBoundingClientRect()), words(a));
            });
            return out;
        });
        expect(centers.length).toBeGreaterThanOrEqual(5);
        expect(Math.max(...centers) - Math.min(...centers)).toBeLessThanOrEqual(1.5);
    });

    // Printed, the page is its figure and its tables, without what navigates them.
    test('prints without the application around it', on('/node'), async ({ page, request, baseURL }) => {
        const key = await firstAction(request, baseURL);
        test.skip(!key, 'no action on this corpus');
        await page.goto(`/node/${encodeURIComponent(key)}`);
        await ready(page);
        await drawn(page);
        await page.emulateMedia({ media: 'print' });
        await expect(page.locator('.mud-appbar')).toBeHidden();
        await expect(page.locator('.abom-scopebar')).toBeHidden();
        await expect(page.locator('[data-abom-figure-canvas] svg.abom-fig')).toBeVisible();
        // The page's own node is tinted, light #fff4e6 against dark #3a3420.
        const fill = await page.locator('svg.abom-fig a.abom-fig-root .abom-fig-box').first()
            .evaluate((r) => getComputedStyle(r).fill);
        expect(fill, 'printed in the light palette').toBe('rgb(255, 244, 230)');
    });
});
