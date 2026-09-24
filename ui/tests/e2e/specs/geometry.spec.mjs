import { test, expect, ready, on, ROUTES, corpusSize, EXPLORE_DRAWN } from '../harness.mjs';

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
    // The drawer took a fixed gutter off every page, and the grid and the graph — the two
    // surfaces needing width most — are the two that paid for it. The whole point of moving
    // navigation into the bar is this number, and it is the one thing bUnit cannot check: it has
    // no CSS engine, so a layout still reserving 240px is green everywhere until somebody looks.
    for (const route of ROUTES) {
        test(`${route} spends its whole width on content`, on(route), async ({ page }) => {
            await page.setViewportSize({ width: 1280, height: 900 });
            await page.goto(route);
            await ready(page);

            const box = await page.evaluate(() => {
                const content = document.querySelector('.mud-main-content');
                const rect = content.getBoundingClientRect();
                return { left: Math.round(rect.left), width: Math.round(rect.width) };
            });

            expect(box.left, `${route} leaves a ${box.left}px gutter to its left`).toBe(0);
            expect(box.width, `${route} content width`).toBe(1280);
        });
    }

    for (const { name, width, height } of WIDTHS) {
        for (const route of ROUTES) {
            test(`${route} does not overflow horizontally at ${name} (${width}px)`, on(route), async ({ page }) => {
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

    test('the coverage strip fills whole rows rather than orphaning a cell', on('/inventory'), async ({
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

    test('no text is clipped by its own container', on('/'), async ({ page }) => {
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

    // A long repository name is the ordinary case, not the edge case: the shared data set carries
    // `globex-industries/partner-integrations` alongside `octo-org/app`. A name that wraps turns a
    // dense row into a three-line block, and thirty of those is a different page.
    test('a long repository name does not wrap its row into a block', on('/inventory?unit=repositories'), async ({
        page,
        request,
        baseURL,
    }) => {
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.seedRepos, 'no rows, so nothing to wrap');

        await page.setViewportSize({ width: 1280, height: 900 });
        await page.goto('/inventory?unit=repositories');
        await ready(page);
        await expect(page.locator('.abom-grid tbody tr td').nth(2)).toBeVisible();

        const rows = await page.evaluate(() => {
            const heights = [...document.querySelectorAll('.abom-grid tbody tr')].map(
                (tr) => Math.round(tr.getBoundingClientRect().height),
            );
            return { heights, median: heights.sort((a, b) => a - b)[Math.floor(heights.length / 2)] };
        });
        expect(rows.heights.length, 'rows to measure').toBeGreaterThan(0);

        // Against the median rather than a constant, because density is a theme decision and this
        // is about one row being unlike the others.
        const tall = rows.heights.filter((h) => h > rows.median * 1.8);
        expect(tall, `row heights ${rows.heights.join(', ')} against a median of ${rows.median}`)
            .toEqual([]);
    });

    test('the graph keeps the room the window has for it, scrolled or not', on(EXPLORE_DRAWN), async ({
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
        await page.goto(EXPLORE_DRAWN);
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
    const UNITS = ['dependencies', 'workflows', 'jobs', 'repositories', 'advisories', 'relationships'];
    const GRIDS = [
        ...UNITS.map((unit) => ({ name: unit, path: `/inventory?unit=${unit}` })),
        ...['catalog', 'runs', 'requests'].map((tab) => ({ name: `CodeQL ${tab}`, path: `/codeql?tab=${tab}` })),
    ];

    for (const { name: unit, path } of GRIDS) {
        test(`${unit} puts none of its columns past the right-hand edge at narrow (1280px)`, on(path), async ({
            page,
            request,
            baseURL,
        }) => {
            const summary = await corpusSize(request, baseURL);
            test.skip(!summary.seedRepos, 'no corpus, so no rows to size the columns');

            await page.setViewportSize({ width: 1280, height: 900 });
            await page.goto(path);
            await ready(page);

            // Wait for a real row. An empty grid renders one cell spanning the table, which fits
            // every width there is — this assertion passed against exactly that while Jobs was
            // 149px over, because /api/jobs takes over two seconds on this corpus.
            const row = page.locator('.abom-grid tbody tr td').nth(3);
            const rows = await row.waitFor({ timeout: 15_000 }).then(() => true, () => false);
            test.skip(!rows, `no rows in ${unit} on this corpus`);

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

            // Squeezing is not fitting; held on CodeQL only, as Inventory workflows rows reach 91px.
            if (!path.startsWith('/codeql')) return;
            const tallest = await page.evaluate(() => Math.max(
                ...[...document.querySelectorAll('.abom-grid .mud-table-container tbody tr')]
                    .filter((tr) => !tr.querySelector('.abom-row-detail'))
                    .map((tr) => tr.getBoundingClientRect().height)));
            expect(Math.round(tallest), `${unit}'s tallest row`).toBeLessThanOrEqual(90);
        });
    }

    // With the last row in view, the header is still on screen and uncovered, whatever scrolled.
    const PINNED = [
        ...['catalog', 'runs', 'requests'].map((t) => ({ name: `CodeQL's ${t}`, path: `/codeql?tab=${t}` })),
        ...[...UNITS, 'coverage'].map((u) => ({ name: `Inventory's ${u}`, path: `/inventory?unit=${u}` })),
    ];
    for (const { name: tab, path } of PINNED) {
        test(`${tab} keeps its column names in view as its rows scroll`, on(path), async ({ page }) => {
            await page.setViewportSize({ width: 1280, height: 720 });
            await page.goto(path);
            await ready(page);

            const grid = page.locator('.abom-grid .mud-table-container').first();
            const present = await grid.waitFor({ timeout: 10_000 }).then(() => true, () => false);
            test.skip(!present, `${tab} has no rows on this corpus to scroll`);
            await expect(grid.locator('tbody tr').first()).toBeVisible();

            const fits = await grid.evaluate((el) => {
                const table = el.querySelector('table');
                return el.getBoundingClientRect().top + table.getBoundingClientRect().height <= innerHeight;
            });
            test.skip(fits, `every row of ${tab} fits in the window on this corpus, so nothing scrolls`);

            const measured = await grid.evaluate(async (el) => {
                // Both boxes scrolled to their far end; on screen means below the fixed bar.
                window.scrollTo(0, document.documentElement.scrollHeight);
                const rows = el.querySelectorAll('tbody tr');
                rows[rows.length - 1].scrollIntoView({ block: 'end' });
                window.scrollTo(0, document.documentElement.scrollHeight);
                await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
                const th = [...el.querySelectorAll('thead th')].find((h) => h.textContent.trim());
                const box = th.getBoundingClientRect();
                const bar = document.querySelector('.mud-appbar').getBoundingClientRect().bottom;
                const hit = document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2);
                return {
                    name: th.textContent.trim(),
                    top: Math.round(box.top),
                    bar: Math.round(bar),
                    bottom: Math.round(box.bottom),
                    innerHeight,
                    covered: hit && !th.contains(hit) ? `${hit.tagName}.${hit.className}`.slice(0, 80) : '',
                };
            });

            expect(measured.top, `"${measured.name}" is under the bar`).toBeGreaterThanOrEqual(measured.bar);
            expect(measured.bottom, `"${measured.name}" is below the window`)
                .toBeLessThanOrEqual(measured.innerHeight);
            expect(measured.covered, `"${measured.name}" is drawn over by`).toBe('');
        });
    }

    // A box edge on a fractional device pixel rounds apart from its pinned header; rows show between.
    test.describe('at a fractional pixel ratio', () => {
        test.use({ deviceScaleFactor: 1.5 });

        for (const { name: tab, path } of PINNED) {
            test(`${tab} shows no sliver of its rows above its pinned header`, on(path), async ({ page }) => {
                await page.setViewportSize({ width: 1280, height: 720 });
                await page.goto(path);
                await ready(page);

                const grid = page.locator('.abom-grid .mud-table-container').first();
                const rows = await grid.locator('tbody tr').first().waitFor({ timeout: 10_000 })
                    .then(() => true, () => false);
                test.skip(!rows, `${tab} has no rows on this corpus`);
                const scrolls = await grid.evaluate((el) => el.scrollHeight > el.clientHeight + 40);
                test.skip(!scrolls, `${tab} has too few rows on this corpus to scroll`);

                // Loud rows, so a sliver of one cannot pass for the header's own color.
                await page.addStyleTag({ content: '.abom-grid tbody td { background-color: rgb(255, 0, 255) !important; }' });

                const slivers = [];
                for (const offset of [0, 0.2, 0.4, 0.6, 0.8]) {
                    const box = await grid.evaluate(async (el, px) => {
                        el.closest('.abom-grid').style.marginTop = `${px}px`;
                        el.scrollTop = 40;
                        await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
                        const r = el.getBoundingClientRect();
                        return { x: r.left, y: r.top, width: el.clientWidth };
                    }, offset);
                    const png = await page.screenshot({ clip: { x: box.x, y: box.y - 3, width: box.width, height: 6 } });
                    const loud = await page.evaluate(async (b64) => {
                        const img = await createImageBitmap(await (await fetch(`data:image/png;base64,${b64}`)).blob());
                        const canvas = new OffscreenCanvas(img.width, img.height);
                        const ctx = canvas.getContext('2d');
                        ctx.drawImage(img, 0, 0);
                        const d = ctx.getImageData(0, 0, img.width, img.height).data;
                        let n = 0;
                        for (let k = 0; k < d.length; k += 4) if (d[k] > 180 && d[k + 1] < 90 && d[k + 2] > 180) n++;
                        return n;
                    }, png.toString('base64'));
                    if (loud) slivers.push(`${loud} pixels of row at a ${offset}px offset`);
                }
                expect(slivers, `${tab} shows its rows above its pinned header`).toEqual([]);
            });
        }
    });

    // Mid-page or nested in a grid's row, so brought to its own last row rather than the page's.
    const INSET = [
        {
            name: "CodeQL's suites",
            at: '/codeql?tab=suites',
            boxes: '[data-abom-suites] .mud-table-container',
            address: async () => '/codeql?tab=suites',
        },
        {
            name: "the largest listed run's results",
            at: '/codeql?tab=runs',
            boxes: '.abom-results .mud-table-container',
            // Asked of the server first: with no run, waiting for one and then for its rows spent
            // the whole test timeout, and a store with nothing to measure failed rather than skipped.
            address: async (request, baseURL) => {
                const { runs } = await (await request.get(`${baseURL}/api/runs`)).json();
                return runs.length > 0 ? '/codeql?tab=runs' : undefined;
            },
            open: async (page, request, baseURL) => {
                const ids = page.locator('[data-abom-run-id]');
                if (!(await ids.first().waitFor({ timeout: 15_000 }).then(() => true, () => false))) return;
                const listed = new Set(await ids.evaluateAll((els) => els.map((e) => Number(e.dataset.abomRunId))));
                const { runs } = await (await request.get(`${baseURL}/api/runs`)).json();
                const size = (r) => Object.values(r.tally).reduce((a, b) => a + b, 0);
                const largest = runs.filter((r) => listed.has(r.rowId)).sort((a, b) => size(b) - size(a))[0];
                await page.locator('tr', { has: page.locator(`[data-abom-run-id="${largest.rowId}"]`) })
                    .locator('[data-abom-run-detail]').first().click();
                await page.locator('[data-abom-run-summary]').waitFor({ timeout: 15_000 });
                const all = page.locator('[data-abom-results-all]');
                if (!(await all.count())) return;
                await all.getByText('Show every repository').click();
                await expect(page.locator('.abom-results thead th', { hasText: 'Status' })).toBeVisible();
            },
        },
        {
            name: "the widest-reaching node's tables",
            at: '/node',
            boxes: '.abom-grid .mud-table-container',
            address: async (request, baseURL) => {
                const { items } = await (
                    await request.get(`${baseURL}/api/actions?top=1&offset=0&sort=dependentRepos&desc=true`)
                ).json();
                return items[0] && `/node/${encodeURIComponent(items[0].nodeKey)}`;
            },
        },
    ];

    for (const { name, at, boxes, address, open } of INSET) {
        test(`${name} keep their column names in view as their rows scroll`, on(at), async ({ page, request, baseURL }) => {
            const path = await address(request, baseURL);
            test.skip(!path, `nothing on this corpus has ${name}`);

            await page.setViewportSize({ width: 1280, height: 720 });
            await page.goto(path);
            await ready(page);
            if (open) await open(page, request, baseURL);

            const rows = page.locator(`${boxes} tbody tr`).first();
            const present = await rows.waitFor({ timeout: 15_000 }).then(() => true, () => false);
            test.skip(!present, `${name} has no rows on this corpus`);

            const measured = await page.evaluate(async (selector) => {
                const bar = document.querySelector('.mud-appbar').getBoundingClientRect().bottom;
                const out = [];
                for (const el of document.querySelectorAll(selector)) {
                    el.scrollIntoView({ block: 'end' });
                    el.scrollTop = el.scrollHeight;
                    await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
                    const th = [...el.querySelectorAll('thead th')].find((h) => h.textContent.trim());
                    if (!th) continue;
                    const box = th.getBoundingClientRect();
                    const hit = document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2);
                    out.push({
                        name: th.textContent.trim().replace(/\s+/g, ' ').slice(0, 40),
                        taller: el.scrollHeight > el.clientHeight + 1 || el.getBoundingClientRect().height > innerHeight - bar,
                        top: Math.round(box.top),
                        bar: Math.round(bar),
                        bottom: Math.round(box.bottom),
                        innerHeight,
                        covered: hit && !th.contains(hit) ? `${hit.tagName}.${hit.className}`.slice(0, 80) : '',
                    });
                }
                return out;
            }, boxes);
            test.skip(!measured.some((m) => m.taller), `every table in ${name} fits in the window on this corpus`);

            for (const m of measured) {
                expect(m.top, `"${m.name}" is under the bar`).toBeGreaterThanOrEqual(m.bar);
                expect(m.bottom, `"${m.name}" is below the window`).toBeLessThanOrEqual(m.innerHeight);
                expect(m.covered, `"${m.name}" is drawn over by`).toBe('');
            }
        });
    }
});
