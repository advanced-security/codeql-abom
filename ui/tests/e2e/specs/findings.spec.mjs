import { test, expect, ready, on } from '../harness.mjs';

// What you own and what it runs are two populations, and an asset's findings must total the same
// in place under either.
for (const scope of ['owned', 'supply-chain'])
for (const [unit, endpoint, identity, kind] of [
    ['dependencies', 'actions', 'nodeKey', 'node'],
    ['workflows', 'workflows', 'nodeKey', 'node'],
    ['jobs', 'jobs', 'nodeKey', 'node'],
    ['repositories', 'repositories', 'fullName', 'repository'],
]) {
    test(`${unit} findings open in place with a matching total under scope=${scope}`, on(`/inventory?unit=${unit}`), async ({ page, request }) => {
        const response = await request.get(`/api/${endpoint}?top=30&scope=${scope}`);
        expect(response.ok()).toBeTruthy();
        const asset = (await response.json()).items.find(item => item.findings > 0);
        test.skip(!asset, 'no asset on the first page has attributed findings');
        const key = asset[identity];
        await page.goto(`/inventory?unit=${unit}&scope=${scope}`);
        await ready(page);
        const control = page.locator(`[data-abom-review-asset=${JSON.stringify(key)}]`);
        await expect(control, 'the API-selected first-page asset renders its findings control').toBeVisible();
        await control.click();
        const drawer = page.locator('[data-abom-asset-review]');
        await expect(drawer).toBeVisible();
        const query = new URLSearchParams({ asset: key, assetKind: kind, scope });
        const review = await (await request.get(`/api/findings?${query}`)).json();
        expect(review.total).toBe(asset.findings);
        await expect(drawer.locator('[data-abom-findings-total]')).toContainText(`${review.total.toLocaleString('en-US')} matching findings`);
        await drawer.locator('[data-abom-review-finding]').first().click();
        await expect(drawer.locator('[data-abom-finding-detail]')).toBeVisible();
        await page.getByRole('button', { name: 'Close asset findings', exact: true }).click();
        await expect(drawer).toHaveCount(0);
        await expect(control).toBeVisible();
    });
}

for (const width of [1440, 390]) {
    test(`Findings retain selection and render recorded help at ${width}px`, on('/inventory?unit=findings'), async ({ page, request }) => {
        const data = await (await request.get('/api/findings?rules=actions%2F*&top=200')).json();
        const finding = data.items.find(item => item.helpAvailable);
        test.skip(!finding, 'no recorded query documentation in this corpus');
        await page.setViewportSize({ width, height: 900 });
        await page.goto(`/inventory?unit=findings&rules=actions%2F*&finding=${finding.id}`);
        await ready(page);
        await expect(page.locator('[data-abom-query-help]')).toBeVisible();
        await expect(page.locator('[data-abom-finding-detail]')).toContainText(finding.title);
        await page.reload();
        await ready(page);
        await expect(page.locator('[data-abom-query-help]')).toBeVisible();
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBeTruthy();
        await page.screenshot({ path: test.info().outputPath(`findings-${width}.png`), fullPage: true });
        await page.getByRole('button', { name: 'Close finding details', exact: true }).click();
        await expect(page.locator('[data-abom-finding-detail]')).toHaveCount(0);
        await expect(page).not.toHaveURL(/finding=\d/);
    });
}