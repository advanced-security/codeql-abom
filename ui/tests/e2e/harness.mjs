import { test as base, expect } from '@playwright/test';

// Every route the interface serves. `/node/{*Key}` is omitted: it needs a node key, so it belongs
// to the populated suite rather than to a list of things that must render from nothing.
//
// Every address the interface actually serves. Nothing retired is kept alive here: this project is
// unpublished, so a question that moved is moved, not forwarded.
export const ROUTES = ['/', '/codeql', '/explore', '/inventory', '/settings'];

// The surfaces, in nav order. Anything else a reader needs is a unit, a tab or an action on one
// of them, and a new entry here means a question grew its own page.
//
// CodeQL earned it: its unit is a query, not a node or a relationship, and findings start there.
export const SURFACES = ['/', '/codeql', '/inventory', '/explore', '/settings'];

// The Explorer draws only a scope somebody chose, so a test that needs a drawing names one. A bare
// `/explore` correctly renders the chooser and no canvas, which is why every spec that went there
// expecting a graph had to be pointed here instead.
//
// The advisory routes with findings rather than the owned closure: owned draws 3,702 nodes, which
// is past the ceiling the view refuses at, so it would produce a refusal rather than a picture.
export const EXPLORE_DRAWN = '/explore?scope=exposure&flags=findings';

// The units Inventory reprojects one scope into. Relationships is absent until an edge carries its
// own identity — see plans/db/edge-facts.md.
export const UNITS = ['dependencies', 'workflows', 'jobs', 'repositories', 'advisories', 'relationships', 'coverage'];

// Blazor reports an unhandled exception by revealing this element rather than by failing the
// navigation, so a page that has thrown still answers 200 and still renders its chrome. Nothing
// short of looking for it notices.
const BLAZOR_ERROR_UI = '#blazor-error-ui';

/**
 * Asks the server what it is holding. The suite branches on the measurement rather than on a flag
 * somebody has to remember to set, because a populated run mislabelled as empty reports its skips
 * as passes.
 */
export async function corpusSize(request, baseURL) {
    const res = await request.get(`${baseURL}/api/summary`);
    expect(res.ok(), 'the server answered /api/summary').toBeTruthy();
    return await res.json();
}

/**
 * Fails if Blazor's error bar became visible, if the page threw, if a request never completed, or
 * if the server answered 5xx.
 *
 * A 4xx is deliberately not a failure, and the line between them is the point: **5xx means ABOM is
 * broken, 4xx means the reader has not done something yet.** Asking for a bill of materials before
 * declaring a scope is the second kind — the page renders an honest empty state for it and the
 * browser still logs the fetch, so banning every console error would make correct first-run
 * behavior indistinguishable from a fault.
 */
export const test = base.extend({
    page: async ({ page }, use, testInfo) => {
        const pageErrors = [];
        const serverFaults = [];
        const failedRequests = [];

        // Attribute each one to the page that was open when it happened. A spec that walks
        // several routes otherwise reports everything against the last one.
        page.on('pageerror', (err) => pageErrors.push(`${page.url()} — uncaught: ${err.message}`));
        page.on('response', (res) => {
            if (res.status() >= 500) serverFaults.push(`${res.status()} ${res.url()}`);
        });
        page.on('requestfailed', (req) => {
            // A navigation the test itself aborted is not a defect.
            if (req.failure()?.errorText === 'net::ERR_ABORTED') return;
            failedRequests.push(`${req.method()} ${req.url()} — ${req.failure()?.errorText}`);
        });

        await use(page);

        if (testInfo.status === 'failed' || testInfo.status === 'timedOut') return;

        const errorBar = page.locator(BLAZOR_ERROR_UI);
        if (await errorBar.count()) {
            expect(await errorBar.isVisible(), `Blazor error bar is showing on ${page.url()}`)
                .toBeFalsy();
        }
        expect(pageErrors, 'uncaught exceptions').toEqual([]);
        expect(serverFaults, 'responses the server reported as its own fault').toEqual([]);
        expect(failedRequests, 'requests that never completed').toEqual([]);
    },
});

/**
 * Waits for Blazor to have rendered.
 *
 * Deliberately not `networkidle`: every open view re-polls on a timer, so "no requests for 500ms"
 * is a property of the poll interval rather than of the page being ready, and it reports a
 * timeout as a layout failure.
 *
 * `.abom-boot` is the static shell in `index.html`, shown until the WASM runtime replaces it.
 * Asserting against a page that still has it up measures the boot screen, and every content
 * assertion then fails for the same misleading reason.
 */
export async function ready(page) {
    await expect(page.locator('.abom-boot')).toBeHidden({ timeout: 30_000 });
    await expect(page.locator('#app')).toBeVisible();
}

export { expect };
