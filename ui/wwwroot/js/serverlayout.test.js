import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
    fitPositions, fitClusters, applyPositions, releasePosition, releaseAll, layoutSummary,
    DEFAULT_EXTENT,
} from './serverlayout.js';

// A 200x100 layout with a node at each corner and one dead centre.
function layout(extra = {}) {
    return {
        producedBy: 'graphviz',
        engine: 'dot',
        width: 200,
        height: 100,
        positions: [
            { key: 'centre', x: 100, y: 50 },
            { key: 'bottom-left', x: 0, y: 0 },
            { key: 'top-right', x: 200, y: 100 },
        ],
        clusters: [{ name: 'cluster_owner_octo', x1: 0, y1: 0, x2: 200, y2: 100 }],
        ...extra,
    };
}

// The canvas is centred on the origin, so a layout that is not re-centred arrives entirely in one
// quadrant and the camera frames empty space.
test('the middle of a layout lands on the origin', () => {
    const placed = fitPositions(layout());
    assert.deepEqual(placed.get('centre'), { x: 0, y: 0 });
});

// Graphviz measures up from the bottom left and the canvas measures down from its centre. Without
// the flip the picture is a mirror of the ranking, so a root drawn at the top arrives at the
// bottom and the direction of the dependency reads backwards.
test('the vertical axis is flipped rather than carried over', () => {
    const placed = fitPositions(layout());
    assert.ok(placed.get('bottom-left').y > 0, 'the engine\'s bottom should be below the centre');
    assert.ok(placed.get('top-right').y < 0, 'the engine\'s top should be above the centre');
});

// Stretching to fill a window would flatten the axis the ranking is expressed on, which is the
// one thing a hierarchical layout is computed for.
test('both axes are scaled by the same factor', () => {
    const placed = fitPositions(layout());
    const corner = placed.get('top-right');
    const scale = DEFAULT_EXTENT / 200;
    assert.equal(corner.x, 100 * scale);
    assert.equal(corner.y, -50 * scale);
});

// A boundary that did not move with its members is a box drawn around the wrong nodes.
test('a cluster box lands on the members it contains', () => {
    const [box] = fitClusters(layout());
    const placed = fitPositions(layout());
    for (const key of ['centre', 'bottom-left', 'top-right']) {
        const at = placed.get(key);
        assert.ok(at.x >= box.x1 && at.x <= box.x2, `${key} is outside its box horizontally`);
        assert.ok(at.y >= box.y1 && at.y <= box.y2, `${key} is outside its box vertically`);
    }
});

// The engine returns no position for a node it never saw. Pinning it anyway would put it at the
// origin, which is a placement nobody computed.
test('a node the layout did not place keeps the simulation', () => {
    const nodes = [{ key: 'centre' }, { key: 'unplaced' }];
    const result = applyPositions(nodes, fitPositions(layout()));

    assert.equal(result.pinned, 1);
    assert.equal(result.free, 1);
    assert.equal(nodes[0].fx, 0);
    assert.equal(nodes[1].fx, undefined, 'an unplaced node must not be pinned');
});

// A computed layout is planar, and a node left free in z drifts out of the plane the ranking was
// computed in.
test('a placed node is pinned in the plane', () => {
    const nodes = [{ key: 'centre' }];
    applyPositions(nodes, fitPositions(layout()));
    assert.equal(nodes[0].fz, 0);
});

// A pinned node that cannot be dragged reads as a broken canvas rather than a deliberate layout.
test('releasing a node hands it back to the simulation', () => {
    const nodes = [{ key: 'centre' }];
    applyPositions(nodes, fitPositions(layout()));
    releasePosition(nodes[0]);
    assert.equal(nodes[0].fx, undefined);
    assert.equal(nodes[0].fy, undefined);
    assert.equal(nodes[0].fz, undefined);

    applyPositions(nodes, fitPositions(layout()));
    releaseAll(nodes);
    assert.equal(nodes[0].fx, undefined);
});

// Nothing else on the canvas distinguishes a layout that will be the same tomorrow from one that
// changes on reload, so the interface has to say which it is showing.
test('the interface can tell a stable layout from a simulated one', () => {
    const stable = layoutSummary(layout(), { pinned: 3, free: 0 });
    assert.equal(stable.stable, true);
    assert.match(stable.text, /dot layout/);

    const degraded = layoutSummary(
        { producedBy: 'none', engine: 'dot', reason: 'layout engine is not installed: dot' });
    assert.equal(degraded.stable, false);
    assert.match(degraded.text, /not installed/);

    // With no reason to give, it still must not read as the stable case.
    const bare = layoutSummary({ producedBy: 'none' });
    assert.equal(bare.stable, false);
    assert.match(bare.text, /change on every reload/);
});

// A partial layout is still a partial answer, and a reader deciding whether to trust the picture
// needs the number rather than a boolean.
test('a partly placed layout says how much the simulation drew', () => {
    const summary = layoutSummary(layout(), { pinned: 8, free: 2 });
    assert.match(summary.text, /2 of 10 placed by the simulation/);
});

// An engine that places nodes but draws no boundary has to say so, or a reader who grouped and
// saw no boxes reads it as a corpus where nothing shared a group.
test('an engine that cannot box a group carries the reason through', () => {
    const summary = layoutSummary(
        layout({ engine: 'sfdp', groupingDrawn: false, reason: 'sfdp draws no cluster boundary' }),
        { pinned: 3, free: 0 });
    assert.match(summary.text, /no cluster boundary/);
});

// An empty or absent layout must not throw on the way to the canvas.
test('an empty layout places nothing rather than failing', () => {
    assert.equal(fitPositions(null).size, 0);
    assert.equal(fitPositions({ positions: [] }).size, 0);
    assert.deepEqual(fitClusters(null), []);

    const nodes = [{ key: 'centre', fx: 5 }];
    const result = applyPositions(nodes, fitPositions(null));
    assert.equal(result.pinned, 0);
    assert.equal(nodes[0].fx, undefined, 'an empty layout releases what a previous one pinned');
});
