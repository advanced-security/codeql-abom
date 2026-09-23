import { test } from 'node:test';
import assert from 'node:assert/strict';

import { tagsOf, grepFor } from './pages.mjs';

// A run scoped to one page or one part of it selects by these, so a test is tagged with what it
// shows — including the part a bare address opens on, which is a part like any other.
test('a bare address is tagged with the part it opens on', () => {
    assert.deepEqual(tagsOf('/codeql'), ['@codeql', '@codeql:requests']);
    assert.deepEqual(tagsOf('/inventory'), ['@inventory', '@inventory:dependencies']);
});

test('a named part is tagged as that part of its page', () => {
    assert.deepEqual(tagsOf('/codeql?tab=runs&run=2'), ['@codeql', '@codeql:runs']);
    assert.deepEqual(tagsOf('/inventory?q=checkout&unit=workflows'), ['@inventory', '@inventory:workflows']);
});

test('a page without parts is tagged as the page alone', () => {
    assert.deepEqual(tagsOf('/'), ['@home']);
    assert.deepEqual(tagsOf('/explore?scope=exposure&flags=findings'), ['@explore']);
    assert.deepEqual(tagsOf('/node/actions%2Fcheckout'), ['@node']);
    assert.deepEqual(tagsOf('/settings'), ['@settings']);
});

// Playwright matches grep against the title with each tag appended after a space.
const titled = (...paths) => `geometry › a test ${[...new Set(paths.flatMap(tagsOf))].join(' ')}`;

test('asking for a page selects every part of it', () => {
    const grep = grepFor(['/codeql']);
    assert.match(titled('/codeql?tab=runs'), grep);
    assert.match(titled('/codeql'), grep);
    assert.doesNotMatch(titled('/inventory'), grep);
});

test('asking for a part selects that part alone', () => {
    const grep = grepFor(['/codeql?tab=runs']);
    assert.match(titled('/codeql?tab=runs'), grep);
    assert.doesNotMatch(titled('/codeql?tab=requests'), grep);
    assert.doesNotMatch(titled('/codeql'), grep);
});

test('a part can be named as its tag, which needs no quoting in a shell', () => {
    assert.match(titled('/inventory?unit=workflows'), grepFor(['inventory:workflows']));
    assert.match(titled('/inventory?unit=workflows'), grepFor(['@inventory:workflows']));
    assert.doesNotMatch(titled('/inventory?unit=jobs'), grepFor(['inventory:workflows']));
});

// A prefix is not a part: "work" names no unit, and selecting workflows by it would be a guess.
test('a prefix of a part selects nothing', () => {
    assert.doesNotMatch(titled('/inventory?unit=workflows'), grepFor(['inventory:work']));
});

test('several pages or parts select any of them', () => {
    const grep = grepFor(['/codeql?tab=runs', 'node']);
    assert.match(titled('/codeql?tab=runs'), grep);
    assert.match(titled('/node/x'), grep);
    assert.doesNotMatch(titled('/settings'), grep);
});
