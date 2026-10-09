import { test, expect, ready, on } from '../harness.mjs';

// "What you own" means what it says: under it, every row the Actions unit lists is yours, by the
// API and on screen. It once listed 77 actions of which 25 were.
test('what you own lists only what you own', on('/inventory?unit=dependencies&scope=owned'), async ({ page, request }) => {
    const data = await (await request.get('/api/actions?top=200&scope=owned')).json();
    test.skip(!data.items?.length, 'no declared repository holds an action in this corpus');
    expect(data.items.filter((item) => !item.owned).map((item) => item.nodeKey)).toEqual([]);

    await page.goto('/inventory?unit=dependencies&scope=owned');
    await ready(page);
    const origins = page.locator('[data-abom-origin]');
    await expect(origins.first()).toBeVisible();
    const words = (await origins.allInnerTexts()).map((w) => w.trim());
    expect(words.filter((w) => w !== 'owned')).toEqual([]);
});

// Both ownership questions are offered, and each says on hover what it holds rather than its name.
test('what you own and what it runs are both offered, each describing itself', on('/inventory'), async ({ page }) => {
    await page.goto('/inventory');
    await ready(page);
    for (const [name, title, holds] of [
        ['owned', 'What you own', /^Only what the repositories you declared hold/],
        ['supply-chain', 'Yours and what it runs', /every action, workflow and image they run/],
    ]) {
        const chip = page.locator(`[data-abom-views] [data-abom-view='${name}']`);
        await expect(chip).toHaveText(title);
        // Its tip, which a reader sees on hover; a title attribute as well would draw a second.
        const tip = page.locator('[data-abom-views] [data-abom-tip]', {
            has: page.locator(`[data-abom-view='${name}']`),
        });
        await expect(tip).toHaveAttribute('data-abom-tip', holds);
        await tip.hover();
        await expect(page.locator('.mud-tooltip', { hasText: holds })).toBeVisible();
    }
});

// A property scope is its own line, saying what it covers as the API counts it, why a held one
// counts nothing, and when it takes in every repository nobody set. Each line stays inside the
// table it belongs to, however long a value is.
test('a property scope says what it covers, why it is held, and what it takes in unasked', on('/settings'), async ({ page, request }) => {
    const declared = await (await request.get('/api/owned-scope')).json();
    const account = declared.find((a) => (a.properties ?? []).length > 0);
    test.skip(!account, 'no account in this corpus is declared by property');

    await page.goto('/settings');
    await ready(page);
    const lines = page.locator(`[data-abom-property-scopes='${account.owner}'] [data-abom-property-scope]`);
    await expect(lines).toHaveCount(account.properties.length);
    const table = await page.locator('.abom-declared-grid').boundingBox();
    for (const [i, clause] of account.properties.entries()) {
        const line = lines.nth(i);
        for (const term of clause.terms) {
            await expect(line).toContainText(`${term.name} = ${term.value}`);
        }
        if (clause.held) {
            await expect(line.locator('[data-abom-scope-held]')).toContainText(clause.heldReason);
            await expect(line.locator('[data-abom-scope-count]')).toHaveCount(0);
        } else {
            await expect(line.locator('[data-abom-scope-count]')).toContainText(new RegExp(`^\\s*${clause.matching} `));
        }
        const warned = (clause.defaults ?? []).length > 0;
        await expect(line.locator('[data-abom-scope-default]')).toHaveCount(warned ? 1 : 0);
        if (warned) await expect(line.locator('[data-abom-scope-default]')).toBeVisible();

        const box = await line.boundingBox();
        expect(box.x + box.width, `scope ${i} runs past the table`).toBeLessThanOrEqual(table.x + table.width + 1);
    }
});
