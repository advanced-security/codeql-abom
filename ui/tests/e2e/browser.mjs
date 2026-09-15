// Which browser the suite runs in, decided once so `make e2e` and a bare `npx playwright test`
// agree, and so a machine with no usable browser says that rather than failing 32 tests.

import { existsSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { homedir, platform } from 'node:os';

/**
 * Picks a channel from what is installed.
 *
 * A request is honored whatever is on disk: a fallback that overrode it would run the suite in a
 * browser the operator did not choose and report that as their result.
 *
 * Otherwise the pinned build wins when it is *complete*. Completeness is the whole point — a
 * download that stalls mid-extraction leaves a directory that looks installed and an app bundle
 * with no framework inside, which fails as a `dlopen` error on every test rather than as a missing
 * browser. That happened here, and it is why this decision exists at all.
 */
export function resolveChannel({ requested, pinnedComplete, chromeInstalled }) {
    if (requested) {
        return { channel: requested, reason: `the channel you asked for (${requested})` };
    }
    if (pinnedComplete) {
        return { channel: 'chromium', reason: 'the pinned build' };
    }
    if (chromeInstalled) {
        return {
            channel: 'chrome',
            reason: 'the system browser, because the pinned build is missing or incomplete',
        };
    }
    throw new Error(
        'e2e: no usable browser. Run `npx playwright install chromium` from ui/tests/e2e, or ' +
            'install Google Chrome and re-run with ABOM_E2E_CHANNEL=chrome.',
    );
}

// What has to exist before a pinned build counts as installed.
//
// More than the launcher, because the launcher is not what fails. A download that stalls
// mid-extraction leaves `Contents/MacOS/Chromium` in place and no
// `Contents/Frameworks/Chromium Framework.framework` beside it, and the process then aborts in
// `dlopen` — which reads as 32 broken tests rather than as a browser that was never installed.
// Measured on this workstation, twice.
const REQUIRED = {
    darwin: [
        ['chrome-mac', 'Chromium.app', 'Contents', 'MacOS', 'Chromium'],
        ['chrome-mac', 'Chromium.app', 'Contents', 'Frameworks', 'Chromium Framework.framework'],
    ],
    linux: [
        ['chrome-linux', 'chrome'],
        ['chrome-linux', 'chrome_crashpad_handler'],
    ],
    win32: [
        ['chrome-win', 'chrome.exe'],
        ['chrome-win', 'chrome.dll'],
    ],
};

/** The paths a complete build must have, for one platform. Empty where we cannot say. */
export function requiredPaths(os) {
    return REQUIRED[os] ?? [];
}

const SYSTEM_CHROME = {
    darwin: '/Applications/Google Chrome.app',
    linux: '/opt/google/chrome/chrome',
    win32: 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
};

function playwrightCache() {
    if (process.env.PLAYWRIGHT_BROWSERS_PATH) return process.env.PLAYWRIGHT_BROWSERS_PATH;
    if (platform() === 'darwin') return join(homedir(), 'Library', 'Caches', 'ms-playwright');
    if (platform() === 'win32') return join(homedir(), 'AppData', 'Local', 'ms-playwright');
    return join(homedir(), '.cache', 'ms-playwright');
}

/** Whether a pinned Chromium is present *and* launchable. */
export function pinnedIsComplete() {
    const cache = playwrightCache();
    const parts = requiredPaths(platform());
    if (!parts.length || !existsSync(cache)) return false;
    let builds = [];
    try {
        builds = readdirSync(cache).filter((d) => d.startsWith('chromium-'));
    } catch {
        return false;
    }
    return builds.some((build) => parts.every((p) => existsSync(join(cache, build, ...p))));
}

export function chromeIsInstalled() {
    const path = SYSTEM_CHROME[platform()];
    return !!path && existsSync(path);
}

/** Resolves from the environment, which is what the Playwright config calls. */
export function detect(requested) {
    return resolveChannel({
        requested,
        pinnedComplete: pinnedIsComplete(),
        chromeInstalled: chromeIsInstalled(),
    });
}
