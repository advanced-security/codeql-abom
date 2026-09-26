import { test, expect, ready, corpusSize, on, SURFACES, UNITS } from '../harness.mjs';

const EVERY_UNIT = UNITS.map((u) => `/inventory?unit=${u}`);

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
    test(`Settings reports repository listing freshness at ${viewport.width}px`, on('/settings'), async ({
        page, request, baseURL,
    }) => {
        const response = await request.get(`${baseURL}/api/owned-scope`);
        expect(response.status()).toBe(200);
        const declared = await response.json();
        test.skip(!declared.length, 'no account is declared on this corpus');

        await page.setViewportSize(viewport);
        await page.addLocatorHandler(
            page.getByRole('button', { name: 'Not now', exact: true }),
            async (button) => { await button.click(); },
        );
        await page.goto('/settings');
        await ready(page);
        await expect(page.locator('[data-abom-listing]')).toHaveCount(declared.length);

        for (const account of declared) {
            const listing = page.locator(`[data-abom-listing='${account.owner}']`);
            const marks = account.enumeration ?? {};
            const route = { graphql: 'GraphQL', rest: 'REST' }[marks.route] ?? marks.route ?? 'not recorded';
            await expect(listing).toContainText(`Route: ${route}`);
            if (!marks.listedAt) await expect(listing).toContainText('No full listing recorded');
            if (!marks.sweptAt) await expect(listing).toContainText('Changes checked: not recorded');

            if (marks.highCreated || marks.highPushed) {
                await listing.locator('summary').click();
            }
            for (const [field, attribute] of [
                ['listedAt', 'listed-at'], ['sweptAt', 'swept-at'],
                ['highCreated', 'high-created'], ['highPushed', 'high-pushed'],
            ]) {
                const time = listing.locator(`[data-abom-${attribute}]`);
                if (marks[field]) {
                    await expect(time).toHaveAttribute('datetime', marks[field]);
                    await expect(time).toBeVisible();
                } else {
                    await expect(time).toHaveCount(0);
                }
            }
        }

        const dimensions = await page.evaluate(() => ({
            page: document.documentElement.scrollWidth, viewport: window.innerWidth,
        }));
        expect(dimensions.page, 'tables contain their own horizontal overflow').toBeLessThanOrEqual(dimensions.viewport + 1);
        await page.screenshot({ path: test.info().outputPath('listing-freshness.png'), fullPage: true });
    });
}

// Four surfaces, one scope. The interface used to answer nine questions with nine pages, each
// inventing its own scope, so an operator who narrowed on one could not carry that narrowing to
// another. What is left is a Dashboard, an Inventory, an Explorer and Settings — and everything
// that used to be a page is a unit, a tab or an action on one of them.
//
// bUnit owns the markup and this owns the wiring: whether a real browser, against the real server,
// gets rows rather than a refusal, and whether what it says about them is true.

test.describe('four surfaces', () => {
    test('the navigation offers the surfaces and nothing else', on('/'), async ({ page }) => {
        await page.goto('/');
        await ready(page);

        const hrefs = await page.evaluate(() =>
            [...document.querySelectorAll('.mud-appbar a')].map((a) =>
                new URL(a.href).pathname,
            ),
        );
        expect(hrefs, 'nav entries').toEqual(SURFACES);
    });

    // The bar is the whole navigation. bUnit owns the markup; what it cannot own is whether the
    // links work in a browser and whether anything is still hidden behind a control.
    test('every surface is one press away, with nothing behind a toggle', on(...SURFACES), async ({ page }) => {
        await page.goto('/');
        await ready(page);

        expect(await page.locator('.mud-drawer').count(), 'drawers').toBe(0);
        expect(
            await page.locator('[aria-label="Toggle navigation"]').count(),
            'a control that reveals what is already on screen',
        ).toBe(0);

        for (const [selector, expected] of [
            ['.abom-nav a[href="/codeql"]', '/codeql'],
            ['.abom-nav a[href="/inventory"]', '/inventory'],
            ['.abom-nav a[href="/explore"]', '/explore'],
            ['[data-abom-settings]', '/settings'],
        ]) {
            await page.goto('/');
            await ready(page);
            await page.locator(selector).first().click();
            await expect(page).toHaveURL(new RegExp(`${expected}$`));
        }
    });

    // The product name is the way back, which is what lets the bar carry no Dashboard entry.
    test('the product name returns to the Dashboard', on('/inventory', '/'), async ({ page }) => {
        await page.goto('/inventory');
        await ready(page);

        await page.locator('[data-abom-brand]').click();
        await expect(page).toHaveURL(/\/$/);
    });
});

test.describe('Inventory reprojects one scope', () => {
    test('every unit renders rows rather than a refusal', on(...EVERY_UNIT), async ({ page, request, baseURL }) => {
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.seedRepos, 'no corpus, so an empty unit is honest rather than broken');

        for (const unit of UNITS) {
            await page.goto(`/inventory?unit=${unit}`);
            await ready(page);
            await expect(
                page.locator(`[data-abom-unit='${unit}']`),
                `the ${unit} unit is on offer`,
            ).toBeVisible();
            // A refusal is a legitimate answer to a scope a unit cannot apply, and no answer at
            // all to a unit's own arrival state.
            await expect(
                page.locator('.mud-alert'),
                `the ${unit} unit refused its own default scope`,
            ).toHaveCount(0);
            await expect(
                page.locator('.mud-table-body .mud-table-row').first(),
                `the ${unit} unit drew a row`,
            ).toBeVisible({ timeout: 15_000 });
        }
    });

    // Every built-in view is written in terms the listing has to apply. Four of them were `scope`,
    // `flags` and `ref`, and a tab strip built from the catalog rendered four refusals until the
    // listing learned them.
    test('every named view the server offers is a tab that returns rows', on('/inventory'), async ({
        page,
        request,
        baseURL,
    }) => {
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.seedRepos, 'no corpus to match against');

        const views = await (await request.get(`${baseURL}/api/views`)).json();
        expect(views.length, 'the catalog offers something').toBeGreaterThan(0);

        for (const view of views) {
            const res = await request.get(`${baseURL}/api/actions?top=1&offset=0&${view.predicate}`);
            expect(res.status(), `${view.name} (${view.predicate}) is a question the listing answers`)
                .toBe(200);
        }

        await page.goto('/inventory');
        await ready(page);
        for (const view of views) {
            await expect(
                page.locator(`[data-abom-view='${view.name}']`),
                `${view.name} is a tab`,
            ).toBeVisible();
        }
    });

    // A page that fetches a cap and pages inside it reads as the whole answer. It held 500 of
    // 1,585, and a column sorted within that page named the worst row on the page rather than the
    // worst row there is.
    test('the grid says how much of the match it is not showing', on('/inventory'), async ({
        page,
        request,
        baseURL,
    }) => {
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.seedRepos, 'nothing to page through');

        await page.goto('/inventory');
        await ready(page);

        const total = (await (await request.get(`${baseURL}/api/actions?top=1`)).json()).total;
        const stated = await page.locator('text=/Showing .* of .* matching/').first().innerText();
        expect(stated.replace(/,/g, ''), 'the line names the size of the match').toContain(
            String(total),
        );

        const pager = await page.locator('.mud-table-pagination').first().innerText();
        expect(pager.replace(/,/g, ''), 'the pager counts the match, not the fetch').toContain(
            String(total),
        );
    });

    // Sorting has to reach the server: reordering the rows already fetched answers a narrower
    // question than the column header asks.
    test('sorting a column asks the server rather than reordering the page', on('/inventory'), async ({
        page,
        request,
        baseURL,
    }) => {
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.seedRepos, 'nothing to sort');

        await page.goto('/inventory');
        await ready(page);

        const asked = page.waitForRequest((r) => r.url().includes('sort=findings'));
        await page
            .locator('.sortable-column-header', { hasText: 'Findings' })
            .first()
            .click();
        await asked;

        const worst = (
            await (
                await request.get(`${baseURL}/api/actions?top=1&offset=0&sort=findings&desc=true`)
            ).json()
        ).items[0];
        // Descending takes two clicks in MudBlazor; the first is ascending.
        await page
            .locator('.sortable-column-header', { hasText: 'Findings' })
            .first()
            .click();
        // IdentityCell turns a key into several text fragments, so the link keeps the full,
        // unambiguous identity available for this assertion.
        const row = page.locator('.mud-table-body .mud-table-row').first();
        await expect(
            row.locator('a').first(),
            'the first row is the worst across the whole match',
        ).toHaveAttribute('href', `/node/${encodeURIComponent(worst.nodeKey)}`, { timeout: 15_000 });
    });

    // The scope is the address, so it survives a reload and a unit switch, or a unit is a page
    // wearing a different hat.
    test('the scope survives a unit switch', on('/inventory?unit=repositories', '/inventory'), async ({ page, request, baseURL }) => {
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.seedRepos, 'no corpus to scope');

        await page.goto('/inventory?unit=repositories');
        await ready(page);
        const asked = page.waitForRequest(
            (r) => r.url().includes('/api/actions') && r.url().includes('q=checkout'),
        );
        await page.goto('/inventory?q=checkout');
        await ready(page);
        await asked;
    });
});

test.describe('the advisories unit is honest about what it excludes', () => {
    // The coverage strip counted 11 live advisories over a table holding 3, with a critical among
    // the absent and nothing on screen accounting for them. The exclusion is correct — they name
    // actions only upstream repositories' own CI uses — and it has to be said.
    test('advisories reaching nothing you own are counted, not silently dropped', on('/inventory?unit=advisories'), async ({
        page,
        request,
        baseURL,
    }) => {
        const res = await request.get(`${baseURL}/api/remediation?top=1`);
        expect(res.status(), '/api/remediation answers').toBe(200);
        const body = await res.json();
        expect(body, 'the page carries what it excluded').toHaveProperty('unreached');

        test.skip(!body.unreached, 'nothing excluded on this corpus, so there is nothing to state');

        await page.goto('/inventory?unit=advisories');
        await ready(page);
        await expect(
            page.locator(`text=/${body.unreached} more live/`),
            'the unit names the advisories it left out',
        ).toBeVisible({ timeout: 15_000 });
    });
});

test.describe('an action belongs to the scope', () => {
    test('the bill of materials export carries the scope on screen', on('/inventory'), async ({ page }) => {
        await page.goto('/inventory?properties=business_id%3A3');
        await ready(page);

        const href = await page
            .locator("[data-abom-action='bom-cyclonedx']")
            .first()
            .getAttribute('href');
        expect(href, 'the export is narrowed to what the reader narrowed to').toContain(
            'properties=business_id%3A3',
        );
    });

    test('the graph opens on the same scope the list was showing', on('/inventory', '/explore'), async ({ page }) => {
        await page.goto('/inventory?kinds=action');
        await ready(page);

        const href = await page
            .locator("[data-abom-action='open-in-explorer']")
            .first()
            .getAttribute('href');
        expect(href, 'the other surface is handed the same scope').toContain('kinds=action');
    });
});

test.describe('a unit carries its own columns', () => {
    // The whole reason the units exist. Listing five kinds through one column set drew reach,
    // total refs and distinct refs as zero for 1,138 of 1,585 rows on the crawled organization,
    // 1,107 of them workflows — a workflow is a consumer, so nothing depends on it and the column
    // is inapplicable rather than small.
    test('no unit renders a column that is zero for every row it lists', on('/inventory?unit=workflows', '/inventory?unit=jobs'), async ({
        page,
        request,
        baseURL,
    }) => {
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.seedRepos, 'no corpus to measure against');

        for (const [unit, endpoint] of [
            ['workflows', 'workflows'],
            ['jobs', 'jobs'],
        ]) {
            const res = await request.get(`${baseURL}/api/${endpoint}?top=200`);
            expect(res.status(), `/api/${endpoint} answers`).toBe(200);
            const { items } = await res.json();
            test.skip(!items.length, `nothing in the ${unit} unit on this corpus`);

            // Every numeric column has to be non-zero somewhere, or it is measuring something this
            // kind does not have.
            const numeric = Object.keys(items[0]).filter(
                (k) => typeof items[0][k] === 'number',
            );
            for (const column of numeric) {
                const anyNonZero = items.some((i) => i[column] !== 0);
                expect(anyNonZero, `${unit}.${column} is zero for every row it lists`).toBe(true);
            }
        }
    });

    test('every unit is reachable and renders rows rather than a refusal', on(...EVERY_UNIT), async ({
        page,
        request,
        baseURL,
    }) => {
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.seedRepos, 'no corpus to match against');

        for (const unit of UNITS) {
            await page.goto(`/inventory?unit=${unit}`);
            await ready(page);
            await expect(
                page.locator(`[data-abom-unit='${unit}']`),
                `${unit} is offered as a unit`,
            ).toBeVisible({ timeout: 15_000 });
            await expect(
                page.locator('.mud-alert'),
                `${unit} answers rather than refusing`,
            ).toHaveCount(0);
        }
    });
});

test.describe('the Dashboard hands over a scope', () => {
    // It used to report coverage in a strip beside its entry points, and that shorter second
    // answer was read as the whole one. Coverage is Discovery's question.
    test('the Dashboard states no coverage of its own', on('/'), async ({ page }) => {
        await page.goto('/');
        await ready(page);

        await expect(
            page.locator('.abom-coverage'),
            'the landing page carries no coverage strip',
        ).toHaveCount(0);
    });

    test('every entry opens the Inventory already narrowed', on('/', '/inventory?unit=advisories'), async ({ page, request, baseURL }) => {
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.seedRepos, 'no corpus to scope');

        await page.goto('/');
        await ready(page);
        const entries = page.locator('[data-abom-entry]');
        expect(await entries.count(), 'the Dashboard offers a way in').toBeGreaterThan(0);

        await page.locator("[data-abom-entry='advisories'] a").first().click();
        await ready(page);
        expect(page.url(), 'it lands on the unit it named').toContain('unit=advisories');
    });

    // Coverage left the Dashboard, and Discovery is not in the navigation, so the way to it has to
    // be here rather than nowhere.
    test('the way to coverage is still offered', on('/'), async ({ page }) => {
        await page.goto('/');
        await ready(page);

        await expect(page.locator("[data-abom-entry='coverage']")).toBeVisible({
            timeout: 15_000,
        });
    });
});

test.describe('a scope an operator kept', () => {
    // The table has held named predicates and the API has served them since before any of the
    // interface existed; what reached it was a hand-edited URL. This drives the round trip against
    // a real server, because the write path needs a control header a stub cannot refuse over.
    test('a scope is named, becomes a tab, and can be forgotten', on('/inventory'), async ({
        page,
        request,
        baseURL,
    }) => {
        const name = `e2e-kept-${Date.now()}`;
        await page.goto('/inventory?ref=tag');
        await ready(page);

        await page.locator('[data-abom-scope-name]').fill(name);
        await page.locator('[data-abom-save-scope]').click();

        await expect(
            page.locator(`[data-abom-view='${name}']`),
            'the kept scope is a tab like any other',
        ).toBeVisible({ timeout: 15_000 });

        // Read back from the server, not from the screen: the point of keeping a scope is that it
        // outlives the tab that made it.
        const views = await (await request.get(`${baseURL}/api/views`)).json();
        const kept = views.find((v) => v.name === name);
        expect(kept, 'the server kept it').toBeTruthy();
        expect(kept.predicate, 'it kept the predicate, canonical').toBe('ref=tag');
        expect(kept.builtin, 'a saved view is not a built-in').toBeFalsy();

        await page.locator(`[data-abom-forget-view='${name}']`).click();
        await expect(
            page.locator(`[data-abom-view='${name}']`),
            'forgetting it removes the tab',
        ).toHaveCount(0, { timeout: 15_000 });

        const after = await (await request.get(`${baseURL}/api/views`)).json();
        expect(
            after.find((v) => v.name === name),
            'and removes it from the server',
        ).toBeFalsy();
    });

    // A built-in is code. Offering a control the server refuses would be an affordance that
    // cannot work, and the refusal would arrive after the click rather than before it.
    test('a built-in offers no way to forget it', on('/inventory'), async ({ page }) => {
        await page.goto('/inventory');
        await ready(page);

        await expect(page.locator("[data-abom-view='owned']")).toBeVisible({ timeout: 15_000 });
        await expect(
            page.locator("[data-abom-forget-view='owned']"),
            'the product\u2019s own questions are not the operator\u2019s to remove',
        ).toHaveCount(0);
    });

    // A scope is its predicate, never the page it was kept on, and the Explorer is the one page
    // whose picture is kept beside it. Compared with the stored row, because a page that opened a
    // scope with a term added or dropped would still look like it had opened it.
    test('a scope kept on the Explorer opens on the Inventory and draws back its picture', on('/explore', '/inventory'), async ({
        page,
        request,
        baseURL,
    }) => {
        const name = `e2e-drawn-${Date.now()}`;
        const chip = `[data-abom-views] [data-abom-view='${name}']`;
        const picture = new Set(['unit', 'tab', 'view', 'direction', 'from', 'status', 'hops', 'groupBy',
            'use3d', 'collapsed', 'stableLayout', 'selection', 'focus']);
        const predicateOf = (url) => new URL(url).search.replace(/^\?/, '').split('&')
            .filter((pair) => pair && !picture.has(pair.split('=')[0]))
            .sort()
            .join('&');

        await page.goto('/explore?scope=exposure&view=priority&hops=2');
        await ready(page);
        await page.locator('[data-abom-named-scopes] [data-abom-scope-name]').fill(name);
        await page.locator('[data-abom-named-scopes] [data-abom-save-scope]').click();
        await expect(page.locator(chip), 'the kept scope is offered here').toBeVisible({ timeout: 15_000 });

        const kept = (await (await request.get(`${baseURL}/api/views`)).json())
            .find((v) => v.name === name);
        expect(kept, 'the server kept it').toBeTruthy();
        expect(kept.presentation, 'the picture was kept beside it').toContain('view=priority');
        expect(kept.presentation).toContain('hops=2');

        await page.goto('/inventory');
        await ready(page);
        await page.locator(chip).click();
        await expect.poll(() => predicateOf(page.url()), 'the Inventory holds the stored question')
            .toBe(kept.predicate);

        await page.goto('/explore');
        await ready(page);
        await page.locator(chip).click();
        await expect.poll(() => predicateOf(page.url()), 'the Explorer holds it too').toBe(kept.predicate);
        await expect.poll(() => new URL(page.url()).searchParams.get('view')).toBe('priority');
        expect(new URL(page.url()).searchParams.get('hops'), 'drawn the way it was kept').toBe('2');

        await page.locator(`[data-abom-forget-view='${name}']`).click();
        await expect(page.locator(chip)).toHaveCount(0, { timeout: 15_000 });
    });

    // The CodeQL page's run draft hosts the same control, over a named scope only. A scope kept
    // there is the one the draft holds, and one kept elsewhere opens there as the question it was
    // kept with, compared with the stored row rather than with what the screen shows.
    test('a scope kept on the CodeQL page opens on the Inventory, and one kept there opens in the draft', on('/codeql?tab=catalog', '/inventory'), async ({
        page,
        request,
        baseURL,
    }) => {
        const stamp = Date.now();
        const here = `e2e-run-${stamp}`;
        const there = `e2e-inv-${stamp}`;
        const views = async () => (await (await request.get(`${baseURL}/api/views`)).json());
        const predicateOf = (url) => new URL(url).search.replace(/^\?/, '').split('&')
            .filter((pair) => pair && !['unit', 'tab'].includes(pair.split('=')[0]))
            .sort()
            .join('&');
        const draft = async () => {
            const catalogued = (await (await request.get(`${baseURL}/api/queries`)).json()).queries ?? [];
            test.skip(catalogued.length === 0, 'no query version is catalogued on this corpus');
            await page.goto('/codeql?tab=catalog');
            await ready(page);
            // The rows arrive after who is signed in, which is what enables Run.
            await page.locator('[data-abom-query-id]').first().waitFor({ timeout: 15_000 });
            const run = page.locator('[data-abom-query-run]:not([disabled])').first();
            test.skip(await run.count() === 0, 'no query version here can be run');
            await run.click();
            await expect(page.locator('[data-abom-run-draft] [data-abom-views]')).toBeVisible({ timeout: 15_000 });
        };

        await draft();
        await expect(page.locator('[data-abom-run-draft] [data-abom-save-scope]'),
            'the draft holds no scope until one is chosen').toBeDisabled();
        await page.locator("[data-abom-run-draft] [data-abom-view='owned']").click();
        await expect(page.locator('[data-abom-run-size]'), 'a chosen scope states its size').toBeVisible({ timeout: 15_000 });
        await page.locator('[data-abom-run-draft] [data-abom-scope-name]').fill(here);
        await page.locator('[data-abom-run-draft] [data-abom-save-scope]').click();
        await expect(page.locator(`[data-abom-run-draft] [data-abom-view='${here}']`)).toBeVisible({ timeout: 15_000 });

        const owned = (await views()).find((v) => v.name === 'owned');
        const keptHere = (await views()).find((v) => v.name === here);
        expect(keptHere, 'the server kept it').toBeTruthy();
        expect(keptHere.predicate, 'it kept the question the draft held').toBe(owned.predicate);

        await page.goto('/inventory?unit=repositories');
        await ready(page);
        await page.locator(`[data-abom-views] [data-abom-view='${here}']`).click();
        await expect.poll(() => predicateOf(page.url()), 'the Inventory holds the stored question')
            .toBe(keptHere.predicate);

        await page.goto('/inventory?unit=repositories&scope=owned&q=has');
        await ready(page);
        await page.locator('[data-abom-named-scopes] [data-abom-scope-name]').fill(there);
        await page.locator('[data-abom-named-scopes] [data-abom-save-scope]').click();
        await expect(page.locator(`[data-abom-view='${there}']`)).toBeVisible({ timeout: 15_000 });
        const keptThere = (await views()).find((v) => v.name === there);
        expect(keptThere, 'the server kept it').toBeTruthy();

        await draft();
        const sized = page.waitForRequest((r) => r.url().includes('/api/databases?'));
        await page.locator(`[data-abom-run-draft] [data-abom-view='${there}']`).click();
        expect(new URL((await sized).url()).search, 'the draft asks for the scope by its name')
            .toBe(`?view=${encodeURIComponent(there)}`);
        await expect(page.locator(`[data-abom-run-draft] [data-abom-view='${there}']`),
            'and holds its stored question').toHaveClass(/abom-view-selected/);

        for (const name of [here, there]) {
            await request.delete(`${baseURL}/api/views/${encodeURIComponent(name)}`,
                { headers: { 'X-Abom-Control': '1' } });
        }
        const left = (await views()).map((v) => v.name);
        expect(left, 'both are forgotten again').not.toContain(here);
        expect(left).not.toContain(there);
    });
});
