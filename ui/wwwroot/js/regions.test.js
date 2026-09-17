import test from 'node:test';
import assert from 'node:assert/strict';

import {
    hullOf, inflate, regionMembers, hullArea, groupSizes, containsPoint, overlapArea, regionPadding,
} from './regions.js';

// A region asserts containment, so its geometry has to be right before anything is drawn with it.
// Every case here is one the real corpus produces: groups of one, groups of two, and members the
// force layout has left in a line.

test('a hull wraps its points and keeps only the ones on the boundary', () => {
    const square = [
        { sx: 0, sy: 0 }, { sx: 10, sy: 0 }, { sx: 10, sy: 10 }, { sx: 0, sy: 10 },
        // Interior points are not on the boundary and must not appear in it.
        { sx: 5, sy: 5 }, { sx: 3, sy: 7 },
    ];
    const hull = hullOf(square);

    assert.equal(hull.length, 4, JSON.stringify(hull));
    for (const corner of [[0, 0], [10, 0], [10, 10], [0, 10]]) {
        assert.ok(hull.some(p => p.x === corner[0] && p.y === corner[1]),
            `corner ${corner} missing from ${JSON.stringify(hull)}`);
    }
});

test('a group too small to have a boundary still returns something drawable', () => {
    // These are not hypothetical: an account with one repository is the common case in the corpus.
    assert.deepEqual(hullOf([]), []);
    assert.equal(hullOf([{ sx: 4, sy: 9 }]).length, 1);
    assert.equal(hullOf([{ sx: 0, sy: 0 }, { sx: 6, sy: 8 }]).length, 2);
});

test('collinear members do not collapse to a line nobody can see', () => {
    // A degenerate hull has zero area, so the drawing pass has to be told rather than left to
    // stroke nothing. Inflation is what gives it a body.
    const line = [{ sx: 0, sy: 0 }, { sx: 5, sy: 5 }, { sx: 10, sy: 10 }];
    const hull = hullOf(line);

    assert.ok(hull.length >= 2, JSON.stringify(hull));
    assert.equal(hullArea(hull), 0);
    assert.ok(hullArea(inflate(hull, 12)) > 0, 'inflation left a boundary with no area');
});

test('inflation pushes the boundary out, never in', () => {
    const hull = hullOf([
        { sx: 0, sy: 0 }, { sx: 20, sy: 0 }, { sx: 20, sy: 20 }, { sx: 0, sy: 20 },
    ]);
    const padded = inflate(hull, 6);

    assert.ok(hullArea(padded) > hullArea(hull),
        `padded ${hullArea(padded)} against ${hullArea(hull)}`);
    // Every original vertex stays inside, or the boundary would cut through the node it wraps.
    const xs = padded.map(p => p.x), ys = padded.map(p => p.y);
    assert.ok(Math.min(...xs) < 0 && Math.min(...ys) < 0);
    assert.ok(Math.max(...xs) > 20 && Math.max(...ys) > 20);
});

// Membership is not this module's to invent: `collapse.js` already decides who folds together, and
// a second rule here is how a region and the collapsed node it corresponds to would come to disagree.
test('membership is grouped by the same rule the collapsed view folds by', () => {
    const nodes = [
        { key: 'octo/app', kind: 'repo', repository: 'octo/app', sx: 0, sy: 0 },
        { key: 'octo/app/.github/workflows/ci.yml', kind: 'workflow', repository: 'octo/app', sx: 5, sy: 5 },
        { key: 'other/tool', kind: 'repo', repository: 'other/tool', sx: 50, sy: 50 },
    ];
    const byOwner = regionMembers(nodes, 'owner');

    assert.deepEqual([...byOwner.keys()].sort(), ['octo', 'other']);
    assert.equal(byOwner.get('octo').length, 2);
    assert.equal(byOwner.get('other').length, 1);
});

test('the ungrouped are not a region, because nothing contains them', () => {
    const nodes = [
        { key: 'octo/app', kind: 'repo', repository: 'octo/app', sx: 0, sy: 0 },
        // An external dependency carries no grouping: a placement is read from the organization,
        // never guessed from an edge. It belongs to no unit and must not be enclosed by one.
        { key: 'actions/checkout', kind: 'action', isExternal: true, sx: 9, sy: 9 },
    ];
    const byGrouping = regionMembers(nodes, 'grouping');

    for (const key of byGrouping.keys()) {
        assert.ok(!/no grouping/i.test(key), `the ungrouped bucket became a region: ${key}`);
    }
});

test('a region needs members to be one', () => {
    assert.equal(regionMembers([], 'owner').size, 0);
    // A node the layout has not placed yet has no position to wrap.
    assert.equal(regionMembers([{ key: 'octo/app', kind: 'repo', repository: 'octo/app' }],
        'owner').size, 0);
});

// A boundary is drawn around what reached the screen, and a reader cannot tell that from a boundary
// around the whole group. The denominator is what makes the two distinguishable.
test('a group is counted whether or not the layout has placed it', () => {
    const sizes = groupSizes([
        { key: 'octo/app', kind: 'repo', repository: 'octo/app', sx: 0, sy: 0 },
        { key: 'octo/lib', kind: 'repo', repository: 'octo/lib' },
        { key: 'other/tool', kind: 'repo', repository: 'other/tool' },
    ], 'owner');

    assert.equal(sizes.get('octo'), 2, 'an unplaced member stopped counting towards its group');
    assert.equal(sizes.get('other'), 1);
});

// A fixed gap in screen pixels is a different thing at every zoom: the nodes shrink as the reader
// zooms out and the gap does not, so the boundary becomes almost entirely margin. A share of the
// members' own extent looks the same however far away the camera is.
test('the gap around a boundary scales with what it encloses', () => {
    const cluster = [{ sx: 0, sy: 0 }, { sx: 200, sy: 0 }, { sx: 200, sy: 150 }, { sx: 0, sy: 150 }];
    const zoomedOut = cluster.map(p => ({ sx: p.sx / 2, sy: p.sy / 2 }));

    const near = regionPadding(cluster, 6, 40);
    const far = regionPadding(zoomedOut, 6, 40);

    assert.ok(Math.abs(near - far * 2) < 0.001, `${near} against ${far}`);
    // As a share of what it wraps, the two boundaries are the same shape.
    assert.ok(near / 200 > 0.05 && near / 200 < 0.2, near);
});

test('a boundary is never thinner than a line nor thicker than a bubble', () => {
    // One member has no extent at all, and still has to be drawable.
    assert.equal(regionPadding([{ sx: 10, sy: 10 }], 6, 40), 6);
    assert.equal(regionPadding([], 6, 40), 6);
    // A group spanning the whole canvas does not get a hundred pixels of margin.
    assert.equal(regionPadding([{ sx: 0, sy: 0 }, { sx: 4000, sy: 0 }], 6, 40), 40);
});

test('a point is inside a boundary or it is not, including on the way past a corner', () => {
    const square = [{ x: 0, y: 0 }, { x: 10, y: 0 }, { x: 10, y: 10 }, { x: 0, y: 10 }];

    assert.equal(containsPoint(square, { x: 5, y: 5 }), true);
    assert.equal(containsPoint(square, { x: 15, y: 5 }), false);
    assert.equal(containsPoint(square, { x: 5, y: -1 }), false);
    // A ray leaving through a vertex is the case a naive crossing count gets wrong.
    assert.equal(containsPoint(square, { x: 5, y: 10 - 1e-9 }), true);
});

test('two boundaries report the area they share, and nothing when they share none', () => {
    const a = [{ x: 0, y: 0 }, { x: 10, y: 0 }, { x: 10, y: 10 }, { x: 0, y: 10 }];
    const b = [{ x: 5, y: 5 }, { x: 15, y: 5 }, { x: 15, y: 15 }, { x: 5, y: 15 }];
    const far = [{ x: 100, y: 100 }, { x: 110, y: 100 }, { x: 110, y: 110 }, { x: 100, y: 110 }];

    assert.equal(overlapArea(a, b), 25);
    assert.equal(overlapArea(a, far), 0);
    assert.equal(overlapArea(a, a), 100);
});

// A region asserts containment. A convex hull over a C-shaped cluster swallows whatever sits in the
// mouth of the C, which states a relationship that does not exist — and the clustering force only
// makes it unlikely.
test('a boundary knows whether it encloses somebody else\'s node', () => {
    const members = [
        { sx: 0, sy: 0 }, { sx: 0, sy: 100 },
        { sx: 400, sy: 0 }, { sx: 400, sy: 100 },
    ];
    const hull = inflate(hullOf(members), 10);

    assert.equal(containsPoint(hull, { x: 200, y: 50 }), true, 'the C no longer has a mouth');
    assert.equal(containsPoint(hull, { x: 200, y: 500 }), false);
});
