import { test } from 'node:test';
import assert from 'node:assert/strict';

import { resolveChannel, requiredPaths } from './browser.mjs';

// Choosing a browser is the one thing standing between this suite and `verify`. It has already
// failed in the way that costs most: the pinned download stalled mid-extraction and left a
// directory that looks installed with an app bundle that cannot launch, so all 32 tests died with
// a `dlopen` error rather than with "no browser". A check for the directory says yes to that.

test('a complete pinned build is what the suite runs', () => {
    assert.deepEqual(resolveChannel({ pinnedComplete: true, chromeInstalled: true }), {
        channel: 'chromium',
        reason: 'the pinned build',
    });
});

// The case that blocked this workstation. Falling back is what makes the suite runnable at all;
// failing here instead would make a half-downloaded browser indistinguishable from a broken UI.
test('a half-extracted pinned build falls back to the system browser', () => {
    const got = resolveChannel({ pinnedComplete: false, chromeInstalled: true });
    assert.equal(got.channel, 'chrome');
    assert.match(got.reason, /pinned/);
});

test('with no browser at all the suite refuses rather than launching nothing', () => {
    assert.throws(() => resolveChannel({ pinnedComplete: false, chromeInstalled: false }), /playwright install/);
});

// An operator who named a channel gets it, complete or not: a fallback that overrode the request
// would run the suite somewhere they did not choose and report it as their result.
test('an explicitly requested channel is never second-guessed', () => {
    const got = resolveChannel({ requested: 'chrome', pinnedComplete: true, chromeInstalled: true });
    assert.equal(got.channel, 'chrome');
    assert.match(got.reason, /asked/);

    const pinned = resolveChannel({ requested: 'chromium', pinnedComplete: false, chromeInstalled: true });
    assert.equal(pinned.channel, 'chromium');
});

// The Makefile always passes the variable and passes it empty when nobody chose. Empty has to mean
// "decide for me", not a channel named "".
test('an empty request means decide, not a channel called nothing', () => {
    assert.equal(resolveChannel({ requested: '', pinnedComplete: true, chromeInstalled: false }).channel, 'chromium');
});

// A pinned Mac Chromium build is incomplete without its framework, even if the launcher exists.
test('completeness means the framework, not just the launcher', () => {
    const mac = requiredPaths('darwin');
    assert.ok(
        mac.some((p) => p.join('/').includes('Frameworks')),
        `darwin requires ${JSON.stringify(mac)}, which a half-extracted bundle satisfies`,
    );
    assert.ok(mac.length > 1, 'one path cannot distinguish a partial extraction from a whole one');
});

test('every supported platform names something to check', () => {
    for (const os of ['darwin', 'linux', 'win32']) {
        assert.ok(requiredPaths(os).length > 0, `${os} has nothing to check`);
    }
    assert.deepEqual(requiredPaths('sunos'), []);
});
