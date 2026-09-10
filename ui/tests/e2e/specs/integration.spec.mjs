import { test, expect, ready, corpusSize, ROUTES } from '../harness.mjs';

// Cross-component integration: every route boots the Blazor runtime, loads its ES modules, calls
// the API and renders. `make js` type-checks none of it and `make ui-test` runs none of it, so a
// bad edit inside graph.js `create()` reaches a reader as a red bar and reaches CI as nothing.

test.describe('every route renders', () => {
    for (const route of ROUTES) {
        test(`${route} boots without an error bar, a console error or a failed request`, async ({ page }) => {
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
    test('the graph canvas is created and sized to its container', async ({ page }) => {
        await page.goto('/explore');
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
});

test.describe('the API answers what the interface asks of it', () => {
    test('a rejected view parameter is an error, not a silent empty graph', async ({ request, baseURL }) => {
        // A predicate that parses and is then ignored returns 200 and an answer that looks real.
        // Six of them shipped that way. An unsupported one must be refused.
        const res = await request.get(`${baseURL}/api/graph/full?scope=owned&notAPredicate=1`);
        expect(res.status(), 'unknown predicate is refused').toBe(400);
    });

    test('the summary the strip renders is the summary the API reports', async ({ page, request, baseURL }) => {
        const summary = await corpusSize(request, baseURL);
        await page.goto('/');
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
