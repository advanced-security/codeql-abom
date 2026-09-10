import { test, expect, ready, ROUTES } from '../harness.mjs';

// Geometry is the layer nothing else owns. bUnit has no CSS engine, so a grid that overflows by
// 288px and a label that wraps onto its own row are both green everywhere until somebody looks.
//
// Each assertion is stated at a width, because that is the only form in which a layout claim can
// be wrong. "It looks fine" is not reproducible; "no horizontal overflow at 1280" is.

const WIDTHS = [
    { name: 'narrow', width: 1280, height: 900 },
    { name: 'wide', width: 1920, height: 1080 },
];

test.describe('geometry', () => {
    for (const { name, width, height } of WIDTHS) {
        for (const route of ROUTES) {
            test(`${route} does not overflow horizontally at ${name} (${width}px)`, async ({ page }) => {
                await page.setViewportSize({ width, height });
                await page.goto(route);
                await ready(page);

                // Read the width actually granted. A requested 1920 can be reported as something
                // else, and a media query that never matched looks exactly like a dead layout.
                const granted = await page.evaluate(() => window.innerWidth);
                expect(granted, `viewport width actually granted for ${route}`).toBe(width);

                const overflow = await page.evaluate(() => {
                    const doc = document.documentElement;
                    return { scroll: doc.scrollWidth, client: doc.clientWidth };
                });
                expect(
                    overflow.scroll,
                    `${route} scrolls ${overflow.scroll - overflow.client}px wider than its viewport`,
                ).toBeLessThanOrEqual(overflow.client);
            });
        }
    }

    test('the coverage strip fills whole rows rather than orphaning a cell', async ({ page }) => {
        // Known to fail, and recorded rather than silenced: with nothing declared the strip
        // renders four items into a three-column grid, so the fourth sits alone on a second row
        // at every page. `plans/ui/theme.md` owns it as "four- and five-item strip wrapping".
        //
        // test.fail rather than skip, because a skip reports as a pass and this will start
        // passing when somebody fixes it — at which point this line is what says so.
        test.fail();

        // The strip's column counts are 2, 3 and 6 because they divide six. A count that does
        // not divide them orphans a cell, on every page, and no markup assertion can see it.
        await page.goto('/');
        await ready(page);

        const strip = page.locator('.abom-coverage').first();
        if (!(await strip.count())) test.skip(true, 'no coverage strip on this page');

        const items = await strip.locator('.abom-coverage-item').count();
        const columns = await strip.evaluate(
            (el) => getComputedStyle(el).gridTemplateColumns.split(' ').filter(Boolean).length,
        );

        expect(columns, 'computed column count').toBeGreaterThan(0);
        expect(
            items % columns,
            `${items} items across ${columns} columns leaves a short final row`,
        ).toBe(0);
    });

    test('no text is clipped by its own container', async ({ page }) => {
        await page.goto('/');
        await ready(page);

        // scrollWidth beyond clientWidth on a leaf element means the text is cut off, which is
        // how a figure silently becomes a different figure.
        const clipped = await page.evaluate(() =>
            [...document.querySelectorAll('.abom-coverage-item, .abom-coverage-value, .mud-chip')]
                .filter((el) => el.scrollWidth > el.clientWidth + 1)
                .map((el) => `${el.className}: "${el.textContent.trim().slice(0, 40)}"`),
        );
        expect(clipped, 'elements whose text is clipped').toEqual([]);
    });
});
