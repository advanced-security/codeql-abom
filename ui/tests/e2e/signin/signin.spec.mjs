import { test, expect } from '@playwright/test';

// The interface with sign-in on. Who signs in is chosen on the test issuer's page, by the person's
// subject: vera is a viewer, otto an operator and ada an administrator.

const issuer = `http://127.0.0.1:${process.env.ABOM_E2E_ISSUER_PORT ?? '9431'}`;

async function signIn(page, person, address = '/') {
    await page.goto(address);
    await page.locator(`[data-person="${person}"]`).click();
    await expect(page.locator('[data-abom-account-login]')).toHaveText(`${person}@example.test`);
}

// Opening ABOM without a session goes straight to the provider: nothing of ABOM is drawn first,
// because drawn for nobody every figure is refused and reads as none.
test('opening ABOM without a session draws nothing of it before signing in', async ({ page }) => {
    const served = [];
    page.on('response', (r) => {
        const url = new URL(r.url());
        if (url.origin !== issuer) served.push(`${r.status()} ${url.pathname}`);
    });

    await page.goto('/inventory?unit=workflows');

    await expect(page.locator('[data-person]').first()).toBeVisible();
    expect(new URL(page.url()).origin).toBe(issuer);
    expect(served.filter((s) => !s.startsWith('302 ')), 'ABOM served something before sign-in').toEqual([]);
});

test('signing in returns to where you were, and the bar says who you are and your role', async ({ page }) => {
    await page.goto('/settings?tab=you');
    await page.locator('[data-person="otto"]').click();

    await expect(page).toHaveURL(/\/settings\?tab=you$/);
    await expect(page.locator('[data-abom-account-login]')).toHaveText('otto@example.test');
    await expect(page.locator('[data-abom-account-bar-role]')).toHaveText('operator');
});

// A viewer is offered what needs a higher role disabled, with the role named, rather than refused on press.
test('a viewer sees the controls a viewer cannot use disabled, naming the role they need', async ({ page }) => {
    await signIn(page, 'vera', '/settings');

    await expect(page.locator('[data-abom-run]')).toBeDisabled();
    await expect(page.locator('[data-abom-crawl-toggle]')).toBeDisabled();
    await expect(page.locator('[data-abom-tip]').filter({ has: page.locator('[data-abom-run]') }))
        .toHaveAttribute('data-abom-tip', /operator role/);
});

test('Settings says who you are to ABOM, what granted your role and what it does not allow', async ({ page }) => {
    await signIn(page, 'vera', '/settings?tab=you');

    const you = page.locator('[data-abom-you="signed-in"]');
    await expect(you).toContainText('vera@example.test');
    await expect(page.locator('[data-abom-you-provider]')).toHaveText('127.0.0.1:9431');
    await expect(page.locator('[data-abom-you-grant]')).toHaveText(['ABOM.Viewer: viewer']);
    await expect(page.locator('[data-abom-you-needs]')).toContainText('Declare the accounts ABOM covers (operator)');
    await expect(page.locator('[data-abom-you-expires]')).toContainText('then you sign in again');
});

test('an administrator is told their role lets them do everything', async ({ page }) => {
    await signIn(page, 'ada', '/settings?tab=you');

    await expect(page.locator('[data-abom-account-bar-role]')).toHaveText('admin');
    await expect(page.locator('[data-abom-you-may]')).toContainText('every operation');
    await expect(page.locator('[data-abom-you-needs]')).toHaveCount(0);
});

// Signing out stays signed out: the page that says so does not sign straight back in through a
// provider that still remembers you.
test('signing out lands on a page that says so, and stays there', async ({ page }) => {
    await signIn(page, 'otto');

    await page.locator('[data-abom-account-avatar]').click();
    await page.locator('[data-abom-sign-out]').click();

    await expect(page).toHaveURL(/\/auth\/\?signin=signed-out$/);
    await expect(page.locator('[data-abom-signin]')).toContainText('You signed out of ABOM');
    await page.waitForTimeout(1500);
    await expect(page).toHaveURL(/\/auth\/\?signin=signed-out$/);
    expect((await page.request.get('/api/me')).status()).toBe(401);
});
