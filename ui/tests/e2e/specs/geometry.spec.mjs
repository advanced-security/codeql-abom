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

    // The catalog's query column takes the width of its ids, so a sentence under one has to wrap
    // inside it. On one line, why a declaration could not be read took the column to 1,365px and
    // pushed every other column off a 1440px screen -- inside a table that scrolls on its own, which
    // the page-level check above cannot see.
    for (const { name, width, height } of WIDTHS) {
        test(`the query catalog shows every column without scrolling at ${name} (${width}px)`,
            on('/codeql?tab=catalog'), async ({ page }) => {
                await page.setViewportSize({ width, height });
                await page.goto('/codeql?tab=catalog');
                await ready(page);
                const settled = page.locator('[data-abom-queries-empty], [data-abom-queries-table]');
                await expect(settled).toBeVisible();
                test.skip(await page.locator('[data-abom-queries-empty]').count() > 0,
                    'nothing is catalogued on this server');

                const sideways = () => page.locator('[data-abom-queries-table] .mud-table-container')
                    .evaluate((el) => el.scrollWidth - el.clientWidth);
                expect(await sideways(), 'the catalog scrolls sideways by default').toBeLessThanOrEqual(1);
                const everything = page.locator('[data-abom-queries-discovery-toggle] input');
                if (await everything.count() > 0) {
                    await everything.check();
                    expect(await sideways(), 'the catalog scrolls sideways showing everything that ran')
                        .toBeLessThanOrEqual(1);
                }
            });
    }

    test('Inventory keeps the global count banner out of the review', on('/inventory'), async ({ page }) => {
        await page.goto('/inventory');
        await ready(page);
        await expect(page.locator('.abom-coverage')).toHaveCount(0);
        await expect(page.locator('[data-abom-unit="findings"]')).toBeVisible();
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

    // GitHub's logo guidelines keep a quarter of the Invertocat's size clear on every side. That is
    // a measurement, so it is checked as one, at every width the bar is laid out at: the name and
    // the navigation are the neighbours that would drift into it.
    for (const { name, width, height } of WIDTHS) {
        test(`the Invertocat keeps its clear space at ${name} (${width}px)`, on('/'), async ({ page }) => {
            await page.setViewportSize({ width, height });
            await page.goto('/');
            await ready(page);

            const intruders = await page.evaluate(() => {
                const mark = document.querySelector('[data-abom-mark]');
                const m = mark.getBoundingClientRect();
                const clear = m.width / 4;
                const zone = {
                    left: m.left - clear, right: m.right + clear, top: m.top - clear, bottom: m.bottom + clear,
                };
                return [...document.querySelectorAll('.mud-appbar *')]
                    .filter((el) => !el.contains(mark) && !mark.contains(el))
                    .filter((el) => {
                        const r = el.getBoundingClientRect();
                        return r.width > 0 && r.height > 0 && r.left < zone.right && r.right > zone.left &&
                            r.top < zone.bottom && r.bottom > zone.top;
                    })
                    .map((el) => `${el.tagName.toLowerCase()}.${[...el.classList].join('.')}: ` +
                        `"${el.textContent.trim().replace(/\s+/g, ' ').slice(0, 30)}"`);
            });
            expect(intruders, "elements inside the Invertocat's clear space").toEqual([]);
        });
    }

    // White on the dark bar and black on the light one, square, and with nothing layered on it,
    // under the pointer as much as at rest. The link's hover used to fade it to 85%, which turns
    // the mark into a grey the guidelines do not offer and is an effect they rule out.
    for (const scheme of ['dark', 'light']) {
        test(`the Invertocat is drawn plainly in ${scheme}, at rest and under the pointer`, on('/inventory'), async ({
            page,
        }) => {
            await page.emulateMedia({ colorScheme: scheme });
            await page.goto('/inventory');
            await ready(page);

            const ink = scheme === 'dark' ? 'rgb(255, 255, 255)' : 'rgb(0, 0, 0)';
            const mark = page.locator('[data-abom-mark]');
            for (const state of ['at rest', 'under the pointer']) {
                if (state === 'under the pointer') {
                    await page.locator('[data-abom-brand]').hover();
                    // A transition would otherwise be read at its first frame and pass.
                    await page.evaluate(() => Promise.all(document.getAnimations()
                        .filter((a) => a instanceof CSSTransition)
                        .map((a) => a.finished.catch(() => {}))));
                }
                const drawn = await mark.evaluate((svg) => {
                    let opacity = 1;
                    const effects = [];
                    for (let el = svg; el && el !== document.body; el = el.parentElement) {
                        const style = getComputedStyle(el);
                        opacity *= Number(style.opacity);
                        if (style.filter !== 'none') effects.push(`${el.tagName} filter: ${style.filter}`);
                        if (style.mixBlendMode !== 'normal') effects.push(`${el.tagName} blend: ${style.mixBlendMode}`);
                    }
                    const own = getComputedStyle(svg);
                    if (own.transform !== 'none') effects.push(`transform: ${own.transform}`);
                    const box = svg.getBoundingClientRect();
                    return {
                        fill: getComputedStyle(svg.querySelector('path')).fill,
                        opacity, effects, width: box.width, height: box.height,
                    };
                });
                expect(drawn.fill, `the Invertocat's ink ${state}`).toBe(ink);
                expect(drawn.opacity, `the Invertocat's opacity ${state}`).toBe(1);
                expect(drawn.effects, `effects on the Invertocat ${state}`).toEqual([]);
                expect(Math.abs(drawn.width - drawn.height), `the Invertocat drawn square ${state}`)
                    .toBeLessThan(0.5);
            }
        });
    }

    // Forced colors repaint the page in a palette the reader chose, and a fill is not among what
    // the browser repaints: measured before this existed, the mark was white on a white bar and
    // black on a black one — gone either way. Legibility comes before the ink, so there it takes
    // the color the browser gives the link it sits in, as the words beside it do.
    for (const [chosen, system] of [['dark', 'light'], ['light', 'dark']]) {
        test(`the Invertocat stays visible under forced colors (${chosen} chosen, ${system} system)`, on('/inventory'), async ({
            page,
        }) => {
            await page.addInitScript((choice) => localStorage.setItem('abom.theme', choice), chosen);
            await page.emulateMedia({ colorScheme: system, forcedColors: 'active' });
            await page.goto('/inventory');
            await ready(page);

            const drawn = await page.locator('[data-abom-mark]').evaluate((svg) => ({
                fill: getComputedStyle(svg.querySelector('path')).fill,
                link: getComputedStyle(svg.closest('a')).color,
            }));
            expect(drawn.fill, "the Invertocat's ink under forced colors").toBe(drawn.link);
        });
    }

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

            // Squeezing is not fitting: a column that fits by stacking its words is not a fit.
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
    // Asked of the API, so an empty corpus skips and a populated grid that renders no row fails.
    const LISTED = {
        dependencies: ['/api/actions?top=1', (d) => d.total],
        workflows: ['/api/workflows?top=1', (d) => d.total],
        jobs: ['/api/jobs?top=1', (d) => d.total],
        repositories: ['/api/repositories?top=1', (d) => d.total],
        advisories: ['/api/remediation?top=1', (d) => d.total],
        relationships: ['/api/relationships?top=1', (d) => d.total],
        coverage: ['/api/frontier?top=1', (d) => d.length],
    };
    for (const { name: tab, path } of PINNED) {
        test(`${tab} keeps its column names in view as its rows scroll`, on(path), async ({ page, request, baseURL }) => {
            const listed = LISTED[new URLSearchParams(path.split('?')[1]).get('unit')];
            if (listed) {
                const answer = await (await request.get(`${baseURL}${listed[0]}`)).json();
                test.skip(!listed[1](answer), `${tab} lists nothing on this corpus`);
            }
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

    // Below 1100px the bar wraps onto a second line, and a box sized for the one-line bar put its
    // pinned header under the bar once the page had scrolled to its end.
    for (const { name: tab, path } of [PINNED[2], PINNED[3]]) {
        test(`${tab} keeps its column names below a bar that has wrapped`, on(path), async ({ page, request, baseURL }) => {
            const listed = LISTED[new URLSearchParams(path.split('?')[1]).get('unit')];
            if (listed) {
                const answer = await (await request.get(`${baseURL}${listed[0]}`)).json();
                test.skip(!listed[1](answer), `${tab} lists nothing on this corpus`);
            }
            await page.setViewportSize({ width: 1000, height: 720 });
            await page.goto(path);
            await ready(page);

            const grid = page.locator('.abom-grid .mud-table-container').first();
            const present = await grid.locator('tbody tr').first().waitFor({ timeout: 10_000 }).then(() => true, () => false);
            test.skip(!present, `${tab} has no rows on this corpus`);

            const measured = await grid.evaluate(async (el) => {
                window.scrollTo(0, document.documentElement.scrollHeight);
                await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
                const th = [...el.querySelectorAll('thead th')].find((h) => h.textContent.trim());
                return {
                    name: th.textContent.trim(),
                    top: Math.round(th.getBoundingClientRect().top),
                    bar: Math.round(document.querySelector('.mud-appbar').getBoundingClientRect().bottom),
                };
            });
            expect(measured.top, `"${measured.name}" is under the wrapped bar`).toBeGreaterThanOrEqual(measured.bar);
        });
    }

    // A pinned header names the columns of the rows under it. An opened request runs to pages of
    // examples, and scrolled into, the box held nothing but the card with the column names pinned
    // over it: labels for values nowhere on screen. The row that opened is pinned under the header
    // while its body is read, so the names and the values they name stay together, and the rows
    // after the body are drawn over that row as they come up rather than under it.
    const OPENED = [
        {
            tab: 'requests',
            items: async (request, baseURL) =>
                (await (await request.get(`${baseURL}/api/query-requests`)).json()).requests ?? [],
            path: (id) => `/codeql?tab=requests&request=${id}`,
        },
        {
            tab: 'runs',
            items: async (request, baseURL) => (await (await request.get(`${baseURL}/api/runs`)).json()).runs ?? [],
            path: (id) => `/codeql?tab=runs&run=${id}`,
        },
    ];
    for (const { tab, items, path } of OPENED) {
        test(`CodeQL's ${tab} keeps the column names and the opened row pinned while its body is read`, on(path(1)), async ({
            page, request, baseURL,
        }) => {
            const listed = await items(request, baseURL);
            test.skip(listed.length === 0, `${tab} lists nothing on this corpus`);
            // Each candidate is opened in turn until one is taller than its box, at each width.
            test.setTimeout(150_000);

            let measured = 0;
            for (const width of [1280, 900]) {
                // Short, so a card on a small corpus is still taller than the box it opens in.
                await page.setViewportSize({ width, height: 560 });

                let tall;
                for (const { rowId } of listed) {
                    await page.goto(path(rowId));
                    await ready(page);
                    const grid = page.locator('.abom-grid .mud-table-container').first();
                    await expect(grid.locator('td.mud-table-child-content')).toBeVisible();
                    const fits = await grid.evaluate((el) =>
                        el.querySelector('td.mud-table-child-content').getBoundingClientRect().height < el.clientHeight + 120);
                    if (!fits) {
                        tall = grid;
                        break;
                    }
                }
                if (!tall) continue;
                measured++;

                const at = (where) => tall.evaluate(async (el, where) => {
                    // The page to its far end, as the pinned-header test does, so the whole box is on screen.
                    window.scrollTo(0, document.documentElement.scrollHeight);
                    const card = el.querySelector('td.mud-table-child-content');
                    const opened = card.parentElement.previousElementSibling;
                    const head = [...el.querySelectorAll('thead th')].find((th) => th.textContent.trim());
                    const box = () => el.getBoundingClientRect();
                    const under = () => head.getBoundingClientRect().bottom;
                    if (where === 'filling') el.scrollTop += card.getBoundingClientRect().top - box().top + 60;
                    if (where === 'above') el.scrollTop += card.getBoundingClientRect().top - under() - 40;
                    if (where === 'end') el.scrollTop = el.scrollHeight;
                    await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
                    const rect = head.getBoundingClientRect();
                    const x = rect.left + rect.width / 2;
                    const hit = document.elementFromPoint(x, rect.top + rect.height / 2);
                    // A sticky row moves its cells; the row's own box stays where it was laid out.
                    const row = opened.querySelector('td').getBoundingClientRect();
                    const rowHit = document.elementFromPoint(x, row.top + row.height / 2);
                    const inView = (tr) => {
                        const r = tr.getBoundingClientRect();
                        return r.bottom > under() + 4 && r.top < box().bottom - 4;
                    };
                    // The grid's own rows: what opened can hold a grid of its own, and its rows are the card's.
                    const all = [...el.querySelectorAll('tbody tr')].filter((tr) => !tr.parentElement.closest('td.mud-table-child-content'));
                    const following = all.slice(all.indexOf(card.parentElement) + 1)
                        .filter((tr) => tr.querySelector('td'))
                        .filter((tr) => {
                            const r = tr.getBoundingClientRect();
                            return r.top >= under() - 0.5 && r.bottom <= box().bottom;
                        });
                    return {
                        name: head.textContent.trim(),
                        labelled: !!hit && head.contains(hit),
                        // What opened is read in the box, not off to the side of a grid wider than it.
                        fits: [...card.children].every((child) => child.getBoundingClientRect().right <= box().right + 1),
                        rows: all.filter((tr) => tr !== card.parentElement && tr.querySelector('td')).filter(inView).length,
                        others: all.filter((tr) => tr !== opened && tr !== card.parentElement && tr.querySelector('td'))
                            .filter(inView).length,
                        pinned: row.top - rect.bottom,
                        shown: !!rowHit && opened.contains(rowHit),
                        covered: following.filter((tr) => {
                            const r = tr.getBoundingClientRect();
                            const h = document.elementFromPoint(x, r.top + r.height / 2);
                            return !h || !tr.contains(h);
                        }).length,
                        following: following.length,
                    };
                }, where);

                const filling = await at('filling');
                expect(filling.others, `${width}px: rows in view while the card fills the box`).toBe(0);
                expect(filling.labelled, `${width}px: "${filling.name}" is covered while its values are read`).toBe(true);
                expect(Math.abs(filling.pinned), `${width}px: the opened row is not pinned under the header`).toBeLessThanOrEqual(2);
                expect(filling.shown, `${width}px: the opened row is drawn under its card`).toBe(true);
                expect(filling.fits, `${width}px: what opened runs past the right edge of its box`).toBe(true);

                const above = await at('above');
                expect(above.rows, `${width}px: rows in view above the card`).toBeGreaterThan(0);
                expect(above.labelled, `${width}px: "${above.name}" is covered while a row is in view`).toBe(true);

                const end = await at('end');
                expect(end.labelled, `${width}px: "${end.name}" is covered at the end of the list`).toBe(true);
                expect(end.covered, `${width}px: rows after the card are drawn under the opened row`).toBe(0);
            }
            test.skip(measured === 0, `no opened card in ${tab} is taller than the box on this corpus`);
        });
    }

    // A run opens onto a grid of its own results, whose rows open in turn. The rule that pins a run
    // under the column names is the run grid's: a result opened inside it is not pinned with it.
    test('CodeQL\'s runs pin their own opened row, not a row opened in a grid inside it', on('/codeql?tab=runs'), async ({
        page, request, baseURL,
    }) => {
        const runs = (await (await request.get(`${baseURL}/api/runs`)).json()).runs ?? [];
        test.skip(runs.length === 0, 'runs lists nothing on this corpus');
        test.setTimeout(90_000);
        let checked = false;
        for (const { rowId } of runs) {
            await page.goto(`/codeql?tab=runs&run=${rowId}`);
            await ready(page);
            const nested = page.locator('td.mud-table-child-content .mud-table-body tr:has(button)').first();
            if (!(await nested.waitFor({ timeout: 10_000 }).then(() => true, () => false))) continue;
            await nested.locator('button').first().click();
            await expect(page.locator('td.mud-table-child-content td.mud-table-child-content').first()).toBeVisible();
            const pinned = await page.evaluate(() => [...document.querySelectorAll('td.mud-table-child-content tr > td:not(.mud-table-child-content)')]
                .map((td) => getComputedStyle(td))
                .filter((style) => style.position === 'sticky' || style.zIndex !== 'auto').length);
            expect(pinned, 'cells of a grid inside an opened run are pinned or layered as the run is').toBe(0);
            checked = true;
            break;
        }
        test.skip(!checked, 'no run here has a result row to open');
    });

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
