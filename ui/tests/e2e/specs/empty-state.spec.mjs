import { test, expect, ready, corpusSize, ROUTES } from '../harness.mjs';

// The states a populated database cannot show. Three entries in the capture matrix needed a
// hand-made database to reach these; a server pointed at an empty file reaches them for free,
// and an empty file is a real state rather than a fabricated one.

test.describe('nothing declared yet', () => {
    test.beforeEach(async ({ request, baseURL }) => {
        const summary = await corpusSize(request, baseURL);
        test.skip(
            summary.seedRepos > 0 || summary.nodes > 0,
            'this suite describes an empty corpus; run it against a fresh database',
        );
    });

    test('an absent declaration reads as "none declared", never as zero', async ({ page }) => {
        await page.goto('/');
        await ready(page);

        const strip = page.locator('.abom-coverage').first();
        if (!(await strip.count())) test.skip(true, 'no coverage strip on this page');

        // innerText puts a newline between a strip value and its label, so the claim has to be
        // made against normalized whitespace or it fails on the layout rather than the wording.
        const text = (await strip.innerText()).replace(/\s+/g, ' ').toLowerCase();
        // "0 yours" asserts a measured emptiness. Nobody has claimed anything, which is a
        // different fact and the only honest one before a declaration exists.
        expect(text, 'the strip distinguishes unclaimed from measured zero')
            .toContain('none declared');
    });

    test('the Explorer says what it is waiting for and names the action', async ({ page }) => {
        await page.goto('/explore');
        await ready(page);

        const body = (await page.locator('body').innerText()).toLowerCase();
        // An empty state is a stage, not an apology: "no results" is indistinguishable from a
        // filter that ran and matched none.
        expect(body, 'the empty Explorer does not stop at "no results"').not.toMatch(/^\s*no results\s*$/);
        expect(body, 'the empty Explorer points somewhere a reader can act')
            .toMatch(/settings|declare|discovery|get started/);
    });

    test('Settings distinguishes "not started" from "paused"', async ({ page }) => {
        await page.goto('/settings');
        await ready(page);

        const body = (await page.locator('body').innerText()).toLowerCase();
        expect(body, 'the crawl states that nothing has started').toMatch(/not started|never started|idle|stopped/);
        expect(body, 'a crawl that never ran is not described as paused').not.toMatch(/pausing/);
    });

    test('no page claims coverage over a corpus that does not exist', async ({ page }) => {
        for (const route of ROUTES) {
            await page.goto(route);
            await ready(page);
            const body = await page.locator('body').innerText();
            // A drained queue once read as complete coverage. With nothing analyzed at all,
            // a percentage is the same lie in a cheaper form.
            expect(body, `${route} claims 100% coverage of an empty corpus`).not.toMatch(/100\s*%/);
        }
    });
});

test.describe('vocabulary', () => {
    test('the retired ownership wording appears nowhere a reader can see it', async ({ page }) => {
        // checkVocabulary guards the source. This guards what actually rendered, including
        // strings the server supplied rather than the Razor.
        for (const route of ROUTES) {
            await page.goto(route);
            await ready(page);
            const body = (await page.locator('body').innerText()).toLowerCase();
            expect(body, `${route} uses retired wording "in scope"`).not.toContain('in scope');
            expect(body, `${route} uses retired wording "under review"`).not.toContain('under review');
            expect(body, `${route} uses a retired boundary spelling`).not.toContain('cross org');
            // Jargon for "everything recorded", and it reached the Dashboard on five cards.
            expect(body, `${route} says "corpus" to a reader`).not.toContain('corpus');
        }
    });
});
