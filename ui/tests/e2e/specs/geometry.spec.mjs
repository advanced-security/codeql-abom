import { test, expect, ready, ROUTES, corpusSize } from '../harness.mjs';

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

    test('the coverage strip fills whole rows rather than orphaning a cell', async ({
        page,
        request,
        baseURL,
    }) => {
        // The strip's column counts are 2, 3 and 6 because they divide six. A count that does
        // not divide them orphans a cell, on every page, and no markup assertion can see it.
        //
        // The defect is empty-state only, and measured: with nothing declared the strip renders
        // four items into three columns and the fourth sits alone; with a corpus behind it there
        // are six across three, which divides. `plans/ui/theme.md` owns it as "four- and five-item
        // strip wrapping".
        //
        // So the expectation branches on what the server is holding, the same way the rest of the
        // suite does. An unconditional `test.fail()` was wrong in the direction that matters: it
        // made a populated run report this as a failure, so the suite could never be green against
        // real data — which is the run that has to gate.
        const summary = await corpusSize(request, baseURL);
        if (!summary.seedRepos) test.fail();

        // The Dashboard hands over a scope and states no coverage of its own, so the strip is not
        // there to measure; Inventory is the nearest surface that carries it.
        await page.goto('/inventory');
        await ready(page);

        const strip = page.locator('.abom-coverage').first();
        expect(await strip.count(), 'the strip renders where this measures it').toBeGreaterThan(0);

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

    test('the graph keeps the room the window has for it, scrolled or not', async ({
        page,
        request,
        baseURL,
    }) => {
        // The graph is the page. `integration.spec.mjs` already checks the canvas matches its
        // container, and that passed throughout a defect where the container itself collapsed to
        // its 360px floor — sized against the document instead of the window, so the further down
        // the reader had scrolled the smaller the graph got. Matching a container proves nothing
        // about whether the container got the room.
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.nodes, 'no graph to size without a corpus');

        await page.setViewportSize({ width: 1440, height: 900 });
        await page.goto('/explore');
        await ready(page);
        await expect(page.locator('.abom-graph-host')).toBeVisible();

        // Scroll, then resize: resize is what re-fits the host, and doing it while scrolled is
        // the state the old arithmetic got wrong.
        const fitted = await page.evaluate(async () => {
            document.documentElement.scrollTop = 300;
            window.dispatchEvent(new Event('resize'));
            await new Promise((r) => setTimeout(r, 500));
            const box = document.querySelector('.abom-graph-host').getBoundingClientRect();
            return {
                height: Math.round(box.height),
                top: Math.round(box.top),
                innerHeight: window.innerHeight,
            };
        });

        // The gutter the module leaves for the caption under the canvas.
        const expected = fitted.innerHeight - Math.max(fitted.top, 0) - 64;
        expect(
            Math.abs(fitted.height - expected),
            `graph is ${fitted.height}px in a window with ${expected}px below its top edge`,
        ).toBeLessThanOrEqual(2);
    });

    // A column past the right-hand edge is not a smaller version of the same page: the reader has
    // no way to know it is there. Measured before this was fixed, Jobs laid out 1,817px of table
    // and put Findings — the column it sorts by — off screen at every width anyone uses.
    const UNITS = ['dependencies', 'workflows', 'jobs', 'repositories', 'advisories'];

    for (const unit of UNITS) {
        test(`${unit} puts none of its columns past the right-hand edge at narrow (1280px)`, async ({
            page,
            request,
            baseURL,
        }) => {
            const summary = await corpusSize(request, baseURL);
            test.skip(!summary.seedRepos, 'no corpus, so no rows to size the columns');

            await page.setViewportSize({ width: 1280, height: 900 });
            await page.goto(`/inventory?unit=${unit}`);
            await ready(page);

            // Wait for a real row. An empty grid renders one cell spanning the table, which fits
            // every width there is — this assertion passed against exactly that while Jobs was
            // 149px over, because /api/jobs takes over two seconds on this corpus.
            await expect(page.locator('.abom-grid tbody tr td').nth(3)).toBeVisible();

            const measured = await page.evaluate(() => {
                const scroller = document.querySelector('.abom-grid .mud-table-container');
                const table = scroller?.querySelector('table');
                if (!table) return null;
                const edge = scroller.getBoundingClientRect().right;
                return {
                    over: Math.round(table.scrollWidth - scroller.clientWidth),
                    hidden: [...table.querySelectorAll('thead th')]
                        .filter((th) => th.getBoundingClientRect().right > edge + 1)
                        .map((th) => th.textContent.trim().replace(/\s+/g, ' '))
                        .filter(Boolean),
                };
            });
            test.skip(!measured, 'no grid on this unit');

            expect(
                measured.hidden,
                `${unit} hides these columns, ${measured.over}px past the edge`,
            ).toEqual([]);
        });
    }
});
