import { test, expect, ready, corpusSize, on, ROUTES, EXPLORE_DRAWN } from '../harness.mjs';
// Cross-component integration: every route boots the Blazor runtime, loads its ES modules, calls
// the API and renders. `make js` type-checks none of it and `make ui-test` runs none of it, so a
// bad edit inside graph.js `create()` reaches a reader as a red bar and reaches CI as nothing.

test.describe('every route renders', () => {
    for (const route of ROUTES) {
        test(`${route} boots without an error bar, a console error or a failed request`, on(route), async ({ page }) => {
            const response = await page.goto(route);
            expect(response?.status(), `HTTP status for ${route}`).toBeLessThan(400);
            await ready(page);
            // The harness asserts the error bar, console and network for every test that gets
            // here; reaching this line with the page rendered is the claim.
            await expect(page.locator('body')).not.toBeEmpty();
        });
    }
});

test.describe('the Explorer draws', () => {
    test('the graph canvas is created and sized to its container', on(EXPLORE_DRAWN), async ({ page }) => {
        await page.goto(EXPLORE_DRAWN);
        await ready(page);

        // force-graph defaults to the window and draws outside its container unless width and
        // height are set explicitly. A canvas wider than its host is that bug, and it is
        // invisible to every other layer.
        const canvas = page.locator('#abom-graph canvas, .abom-graph canvas, canvas').first();
        await expect(canvas, 'the Explorer created a canvas').toBeVisible({ timeout: 20_000 });

        const fits = await canvas.evaluate((el) => {
            const host = el.parentElement;
            return {
                canvas: Math.round(el.getBoundingClientRect().width),
                host: Math.round(host.getBoundingClientRect().width),
            };
        });
        expect(fits.canvas, 'canvas width against its host').toBeLessThanOrEqual(fits.host + 1);
        expect(fits.canvas, 'canvas was given a real width').toBeGreaterThan(0);
    });

    test('a view that has come to rest still answers the pointer', on(EXPLORE_DRAWN), async ({ page, request, baseURL }) => {
        // At rest `drawLabels` stops rescheduling and the renderer is paused, so nothing polls the
        // camera. The same drag once moved 97% of the pixels on a fresh view and 0% on a settled
        // one, and no unit test can see it: the wake path is only real inside a browser.
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.nodes, 'nothing to settle without a corpus');

        await page.goto(EXPLORE_DRAWN);
        await ready(page);

        // Inside the suite's own budget, so a slow settle reports what did not happen rather than
        // a bare test timeout. Measured at ten seconds on the reference corpus.
        // `expect.poll`, not `waitForFunction`: an async predicate returns a promise, which
        // `waitForFunction` treats as truthy and resolves against immediately — it reported the
        // view as settled before the layout had started.
        await expect.poll(() => restStateOf(page), {
            timeout: 22_000,
            message: 'the view never came to rest',
        }).toBe('still');

        const box = await page.locator('.abom-graph-host').boundingBox();
        const before = await cameraOf(page);
        await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
        await page.mouse.down();
        await page.mouse.move(box.x + box.width / 2 + 120, box.y + box.height / 2 + 60, { steps: 8 });
        await page.mouse.up();
        await page.waitForTimeout(600);

        const after = await cameraOf(page);
        expect(after, 'the camera did not move for a drag on a settled view').not.toEqual(before);
    });
});

/** What the label loop thinks the view is doing: `none`, `idle` or `still`. */
async function restStateOf(page) {
    return page.evaluate(async () => {
        const { instances } = await import('/js/state.js');
        const it = instances.get('abom-graph');
        return it ? it.resting : 'gone';
    });
}

/** Where the camera is, which is the only proof a gesture reached the scene. */
async function cameraOf(page) {
    return page.evaluate(async () => {
        const { instances } = await import('/js/state.js');
        const c = instances.get('abom-graph').graph.camera();
        return [c.position.x, c.position.y, c.position.z].map((v) => Math.round(v));
    });
}

test.describe('analysis modes', () => {
    for (const surface of [
        { route: '/inventory', endpoint: '/api/actions' },
        { route: '/explore', endpoint: '/api/graph/full' },
    ]) {
        test(`${surface.route} selects a shape, reloads it and keeps the control visible`, on(surface.route), async ({ page }, testInfo) => {
            // The Explorer draws only a scope somebody chose, so one is named on the way in.
            // Without it there is no graph to request a shape for.
            await page.goto(
                surface.route === '/explore'
                    ? `${surface.route}?scope=supply-chain&flags=findings`
                    : surface.route,
            );
            await ready(page);

            // Both surfaces set the shape through the shared filter picker, where it is a term
            // like any other. What each has to show is the same thing: a way in before, and the
            // choice still legible after a reload.
            const entry = page.locator('[data-abom-add-filter]');
            const applied = page.locator('[data-abom-filter-chip="mode"]');
            await expect(entry).toBeVisible();

            const chooseExtended = async () => {
                await entry.click();
                await page.locator('[data-abom-term="mode"]').click();
                await page.locator('[data-abom-value="extended"]').click();
            };

            const isExtended = (response) => {
                const address = new URL(response.url());
                return address.pathname === surface.endpoint && address.searchParams.get('mode') === 'extended';
            };
            const selected = page.waitForResponse(isExtended);
            await chooseExtended();
            const response = await selected;
            expect(response.status()).toBe(200);
            await expect(page).toHaveURL(/mode=extended/);
            const answer = await response.json();
            if (surface.endpoint === '/api/actions') {
                await expect(page.getByText(/Showing .* of .* matching/).first())
                    .toContainText(`of ${answer.total.toLocaleString('en-US')} matching`);
            } else {
                const keys = new Set(answer.nodes.map((node) => node.key));
                for (const edge of answer.edges) {
                    expect(keys.has(edge.source) && keys.has(edge.target)).toBe(true);
                }
            }

            const reloaded = page.waitForResponse(isExtended);
            await page.reload();
            await ready(page);
            expect((await reloaded).status()).toBe(200);
            await expect(page).toHaveURL(/mode=extended/);
            await page.screenshot({ path: testInfo.outputPath('analysis-mode-desktop.png') });
            await page.setViewportSize({ width: 390, height: 844 });
            await expect(applied).toBeVisible();
            await applied.scrollIntoViewIfNeeded();
            const geometry = await applied.evaluate((element) => {
                const bounds = element.getBoundingClientRect();
                return { left: bounds.left, right: bounds.right, viewport: innerWidth };
            });
            expect(geometry.left).toBeGreaterThanOrEqual(0);
            expect(geometry.right).toBeLessThanOrEqual(geometry.viewport);
            await page.screenshot({ path: testInfo.outputPath('analysis-mode-mobile.png') });

            const regular = page.waitForResponse((reply) => {
                const address = new URL(reply.url());
                return address.pathname === surface.endpoint && address.searchParams.get('mode') !== 'extended';
            });
            // Back to regular: the shape is a term on both surfaces now, so going back means
            // removing it. Absent is regular, which is the whole point of not writing a default
            // nobody asked for.
            await applied.locator('[data-abom-chip-remove]').click();
            expect((await regular).status()).toBe(200);
            await expect(page).not.toHaveURL(/mode=extended/);
        });
    }
});

test.describe('the API answers what the interface asks of it', () => {
    test('a rejected view parameter is an error, not a silent empty graph', on('/explore'), async ({ request, baseURL }) => {
        // A predicate that parses and is then ignored returns 200 and an answer that looks real.
        // Six of them shipped that way. An unsupported one must be refused.
        const res = await request.get(`${baseURL}/api/graph/full?scope=owned&notAPredicate=1`);
        expect(res.status(), 'unknown predicate is refused').toBe(400);
    });

    test('the summary the strip renders is the summary the API reports', on('/inventory'), async ({ page, request, baseURL }) => {
        const summary = await corpusSize(request, baseURL);
        // Not the Dashboard: that surface hands over a scope and states nothing else, because a
        // second, shorter coverage answer beside the entry points was read as the whole one.
        await page.goto('/inventory');
        await ready(page);

        const strip = page.locator('.abom-coverage').first();
        if (!(await strip.count())) test.skip(true, 'no coverage strip on this page');

        const text = (await strip.innerText()).replace(/,/g, '');
        // Whatever else the strip says, it must not invent a repository count.
        if (summary.seedRepos > 0) {
            expect(text, 'the strip names the corpus figure the API reports')
                .toContain(String(summary.seedRepos));
        }
    });
});
