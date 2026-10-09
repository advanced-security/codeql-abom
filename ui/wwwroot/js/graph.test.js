import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
    seed, repoOf, orgOf, edgeClass, boundaryClass, groupOf, anchorKey, keyOf, faded,
    nodeRadius, isSelected, particleSpeed, needsRender, collapseGroups, ungroupedName,
    mergeInto, placeArrivals, neighborhood, discoveryColor, escapesGraph,
    simulationSet, visibleSignature, restLevel, REST_DELAY, REST_FPS, fullyAnalyzed, labelFor,
    graphHeight, visibilityGate, zoomTarget, refitNeeded, labelTiers, leafLabel,
    haloAlpha, nodeContext, regionLabelParts, regionLabelSpot, regionShapes, regionCaption,
    drawnAsRegion, nodeBrief, clampToCanvas, shortRef, isDarkBackground, settledRegions, buttonMap,
    containmentIntoRegion, hoveredRegion, fitDistance, coreBounds, gravityForce, spreadFor, seedAt,
    fitMargin, nearestTo, withDependencies, clickAction, scopeFilters, isClick, answersScope,
    focusKeys, viewCaption, drawDecision, DRAW_CEILING, answersOrigins,
} from './graph.js';
import { withAlpha, coverageColor } from './appearance.js';import { hullOf, inflate, containsPoint, overlapArea, groupSizes } from './regions.js';

// Pure decisions behind the view: edge classes, node size, and grouping.

test('an edge is classified by the closest true relationship', () => {
    const a = { key: 'octo/app/.github/workflows/ci.yml', repository: 'octo/app' };
    const b = { key: 'octo/app/.github/actions/build', repository: 'octo/app' };
    const c = { key: 'octo/other/.github/workflows/ci.yml', repository: 'octo/other' };
    const d = { key: 'actions/checkout' };

    assert.equal(edgeClass(a, b), 'sameRepo');
    // Same repository outranks same owner.
    assert.equal(edgeClass(a, c), 'sameOwner');
    assert.equal(edgeClass(a, d), 'crossOwner');
});

test('an owner node compares by its account and not by a repository it does not have', () => {
    const org = { key: 'org:actions', kind: 'org', owner: 'actions' };
    const action = { key: 'actions/checkout' };
    const foreign = { key: 'docker/build-push-action' };

    assert.equal(orgOf(org), 'actions');
    assert.equal(repoOf(org), '', 'an account owns repositories rather than being one');
    assert.equal(edgeClass(org, action), 'sameOwner');
    assert.equal(edgeClass(org, foreign), 'crossOwner');
});

// Stored boundaries are authoritative; the browser derives only when they are absent.
test('a stored boundary is preferred over deriving one again in the browser', () => {
    const s = { key: 'octo/app/.github/workflows/ci.yml', repository: 'octo/app' };
    const t = { key: 'octo/app/.github/workflows/build.yml', repository: 'octo/app' };

    // Contradicts edgeClass so preferring the stored value is visible.
    assert.equal(edgeClass(s, t), 'sameRepo');
    assert.equal(boundaryClass('cross-owner', s, t), 'crossOwner');
    assert.equal(boundaryClass('same-owner', s, t), 'sameOwner');
    assert.equal(boundaryClass('same-repository', s, t), 'sameRepo');
});

// Containment and merged collapsed edges have no stored boundary, so they fall back to endpoints.
test('an edge with no stored boundary still gets one from its endpoints', () => {
    const org = { key: 'org:actions', kind: 'org', owner: 'actions' };
    const action = { key: 'actions/checkout' };

    assert.equal(boundaryClass('', org, action), 'sameOwner');
    assert.equal(boundaryClass(undefined, org, action), 'sameOwner');
    // Unrecognized values are not filterable classes.
    assert.equal(boundaryClass('something-else', org, action), 'sameOwner');
});

test('grouping by workflow does not put every node in a group of its own', () => {
    // Steps inherit their workflow container before grouping.
    const workflow = { key: 'octo/app/.github/workflows/ci.yml', kind: 'workflow' };
    const step = { key: 'step:1', kind: 'step', container: 'octo/app/.github/workflows/ci.yml' };
    const action = { key: 'actions/checkout', kind: 'action' };

    assert.equal(groupOf(workflow, 'workflow'), workflow.key);
    assert.equal(groupOf(step, 'workflow'), workflow.key, 'a step belongs to the file holding it');
    assert.equal(groupOf(action, 'workflow'), '',
        'an action is referenced by workflows rather than living in one');
});

test('grouping by owner and by repository agree with the node key', () => {
    const wf = { key: 'octo/app/.github/workflows/ci.yml', repository: 'octo/app', kind: 'workflow' };
    assert.equal(groupOf(wf, 'owner'), 'octo');
    assert.equal(groupOf(wf, 'repo'), 'octo/app');
    assert.equal(groupOf(wf, 'none'), '');
});

test('a group anchors on a node that exists, so grouping cannot fight the layout', () => {
    const wf = { key: 'octo/app/.github/workflows/ci.yml', repository: 'octo/app', kind: 'workflow' };
    assert.equal(anchorKey(wf, 'owner'), 'org:octo');
    assert.equal(anchorKey(wf, 'repo'), 'octo/app');
    assert.equal(anchorKey({ key: 'org:octo', kind: 'org', owner: 'octo' }, 'repo'), '');
});

test('size carries reach, and selecting a node barely changes it', () => {
    const hub = { key: 'actions/setup-python', kind: 'action', dependentRepos: 66 };
    const leaf = { key: 'octo/app/.github/workflows/ci.yml', kind: 'workflow', dependentRepos: 0 };

    assert.ok(nodeRadius(hub) > nodeRadius(leaf), 'reach must be visible as size');

    const selected = nodeRadius(hub, new Set([hub.key]));
    assert.ok(selected / nodeRadius(hub) < 1.5, `selected node grew ${selected / nodeRadius(hub)}x`);
});

test('an owner is sized as a container rather than by the reach of its largest member', () => {
    const org = { key: 'org:actions', kind: 'org', dependentRepos: 202 };
    const action = { key: 'actions/checkout', kind: 'action', dependentRepos: 202 };
    assert.ok(nodeRadius(org) < nodeRadius(action));
});

test('selection accepts a set or a single key', () => {
    assert.equal(isSelected(new Set(['a']), 'a'), true);
    assert.equal(isSelected(new Set(['a']), 'b'), false);
    assert.equal(isSelected('a', 'a'), true);
    assert.equal(isSelected(null, 'a'), false);
});

test('a link endpoint reads the same before and after the renderer rewrites it', () => {
    // force-graph replaces endpoint keys with node objects once seeded.
    assert.equal(keyOf('actions/checkout'), 'actions/checkout');
    assert.equal(keyOf({ key: 'actions/checkout' }), 'actions/checkout');
});

test('fading an edge changes its alpha and keeps its hue', () => {
    assert.equal(faded('rgba(255,110,156,0.85)'), 'rgba(255,110,156,0.06)');
    assert.equal(faded('rgb(255,110,156)'), 'rgba(255,110,156,0.06)');
    assert.equal(faded('#ff6e9c'), '#ff6e9c', 'a hex colour has no alpha to drop');
});

test('layout and animation are deterministic for the same corpus', () => {
    // Positions are seeded by key so the same graph lays out identically.
    assert.equal(seed('actions/checkout'), seed('actions/checkout'));
    assert.notEqual(seed('actions/checkout'), seed('actions/cache'));

    const link = { source: 'a', target: 'b' };
    assert.equal(particleSpeed(link), particleSpeed(link));
});

test('particles move slowly enough to read as drift rather than a pulse', () => {
    const speeds = [];
    for (let i = 0; i < 200; i++) {
        speeds.push(particleSpeed({ source: `n${i}`, target: 'actions/setup-python' }));
    }
    const slowest = Math.min(...speeds);
    const fastest = Math.max(...speeds);

    // A traverse under about four seconds at 60fps reads as a pulse.
    assert.ok(1 / (fastest * 60) > 4, `fastest traverse is ${(1 / (fastest * 60)).toFixed(1)}s`);
    // Distinct periods are what pull them out of lockstep; there is no phase offset available.
    assert.ok(fastest / slowest > 2, `speed spread is only ${(fastest / slowest).toFixed(1)}x`);
});

test('a collapsed graph is rebuilt whenever anything it is folded from changes', () => {
    // The fold is built inside render(), so collapsed data must rebuild with filter changes.
    const collapsed = { seeded: true, collapsed: true, groupBy: 'owner' };
    assert.equal(needsRender(collapsed), true);

    // The fold depends on the filtered edges too, so an unchanged grouping is not enough to
    // stay on the cheap path.
    assert.equal(needsRender({ ...collapsed, foldedBy: 'owner' }), true);
});

test('an expanded graph is not rebuilt just because the grouping changed', () => {
    // Grouping only moves an expanded graph, so it must stay on the cheap path.
    const it = {
        seeded: true, collapsed: false, groupBy: 'owner', foldedBy: null,
        visibleSig: '3:1', renderedSig: '3:1',
    };
    assert.equal(needsRender(it), false);
    assert.equal(needsRender({ ...it, seeded: false }), true, 'a first draw always renders');
});

test('a different grouping folds to a different set of groups', () => {
    const nodes = [
        { key: 'octo/app/.github/workflows/ci.yml', kind: 'workflow', repository: 'octo/app' },
        { key: 'octo/lib/.github/workflows/ci.yml', kind: 'workflow', repository: 'octo/lib' },
    ];
    const byOwner = collapseGroups({ groupBy: 'owner' }, nodes, []);
    const byRepo = collapseGroups({ groupBy: 'repo' }, nodes, []);

    assert.equal(byOwner.nodes.length, 1, 'both repositories belong to one owner');
    assert.equal(byRepo.nodes.length, 2, 'each repository is its own group');
});

// The whole collapsed view was invisible: 24 groups counted in the header, nothing drawn but one
// merged edge between two endpoints that were never painted. The fold replaces member keys with
// group keys, and the draw gate went on testing against the members, which matches nothing.
// Double-clicking empty space zooms in, so the arithmetic behind it has to move the camera
// closer without turning it: a zoom that rotates loses the reader's place in a view where most
// nodes look alike.
test('a zoom changes how far the camera is, never which way it points', () => {
    const target = { x: 0, y: 0, z: 0 };
    const cam = { x: 0, y: 0, z: 1000 };

    const closer = zoomTarget(cam, target, 0.6);
    assert.equal(Math.round(closer.z), 600, 'zooming in shortens the distance by the factor');

    const angled = { x: 300, y: 400, z: 0 };
    const zoomed = zoomTarget(angled, target, 0.5);
    assert.equal(Math.round(Math.hypot(zoomed.x, zoomed.y, zoomed.z)), 250);
    // Same bearing: the ratio of the components is unchanged.
    assert.equal(Math.round(zoomed.x / zoomed.y * 100), Math.round(300 / 400 * 100));
});

test('a zoom orbits whatever the camera is looking at, not the origin', () => {
    const target = { x: 500, y: 0, z: 0 };
    const cam = { x: 500, y: 0, z: 200 };

    const closer = zoomTarget(cam, target, 0.5, 10);
    assert.deepEqual(
        { x: Math.round(closer.x), y: Math.round(closer.y), z: Math.round(closer.z) },
        { x: 500, y: 0, z: 100 },
        'a camera already panned away from the origin zooms toward what it is framing');
});

test('zooming in repeatedly stops before it passes through the scene', () => {
    let cam = { x: 0, y: 0, z: 1000 };
    for (let i = 0; i < 40; i++) cam = zoomTarget(cam, { x: 0, y: 0, z: 0 }, 0.6, 80);

    assert.equal(cam.z, 80, 'the floor holds');
    assert.ok(cam.z > 0, 'the camera never crosses the target and inverts the view');
});

test('a collapsed view draws the groups it folded, not the keys they replaced', () => {
    const nodes = [
        { key: 'octo/app/.github/workflows/ci.yml', kind: 'workflow', repository: 'octo/app' },
        { key: 'other/lib/.github/workflows/ci.yml', kind: 'workflow', repository: 'other/lib' },
    ];
    const it = {
        groupBy: 'owner',
        collapsed: true,
        visible: new Set(nodes.map(n => n.key)),
    };
    const fold = collapseGroups(it, nodes, []);
    assert.ok(fold.nodes.length > 0, 'the fixture folds to something');

    const gate = visibilityGate(it);
    for (const g of fold.nodes) {
        assert.ok(!gate || gate.has(g.key),
            `${g.key} was folded and then hidden, so the view counts it and never draws it`);
    }

    // Why the gate cannot simply be `it.visible`: a group key is not one of the keys it replaced.
    for (const g of fold.nodes) {
        assert.equal(it.visible.has(g.key), false);
    }

    // And an expanded view still gates on the filter, or filtering would stop narrowing.
    assert.equal(visibilityGate({ ...it, collapsed: false }), it.visible);
});

test('owned nodes group by repository grouping and nothing else claims one', () => {
    // The one grouping that cannot be derived from a node's key. It is a property of a repository
    // inside the owned organization, so the server labels the node and the browser reads the
    // label rather than parsing it out of the key.
    const nodes = [
        { key: 'octo/app/.github/workflows/ci.yml', kind: 'workflow', repository: 'octo/app', grouping: '3' },
        { key: 'octo/pay/.github/workflows/ci.yml', kind: 'workflow', repository: 'octo/pay', grouping: '3' },
        { key: 'octo/web/.github/workflows/ci.yml', kind: 'workflow', repository: 'octo/web', grouping: '7' },
        { key: 'actions/checkout', kind: 'action', repository: '' },
    ];

    const byGroup = collapseGroups({ groupBy: 'grouping' }, nodes, []);
    const names = byGroup.nodes.map(n => n.path).sort();

    assert.deepEqual(names, ['(no grouping)', '3', '7']);
    assert.equal(byGroup.nodes.find(n => n.path === '3').members, 2,
        'two repositories carrying the same value fold together');
});

test('a dependency several groups reach stays one node with an edge from each', () => {
    // The chain leaving an owned node is the reason to draw this view at all, so a dependency
    // keeps its edges to every group that reaches it. Duplicating it into each group would invent
    // a placement, and dropping it for having none of its own would hide the supply chain.
    const nodes = [
        { key: 'octo/app/.github/workflows/ci.yml', kind: 'workflow', repository: 'octo/app', grouping: '3' },
        { key: 'octo/web/.github/workflows/ci.yml', kind: 'workflow', repository: 'octo/web', grouping: '7' },
        { key: 'actions/checkout', kind: 'action', repository: '' },
    ];
    const links = [
        { source: nodes[0].key, target: nodes[2].key, shown: true },
        { source: nodes[1].key, target: nodes[2].key, shown: true },
    ];

    const byGroup = collapseGroups({ groupBy: 'grouping' }, nodes, links);

    assert.equal(byGroup.nodes.filter(n => n.path === '(no grouping)').length, 1,
        'one bucket, not one per group that reaches it');
    assert.equal(byGroup.links.length, 2, 'both groups keep their edge to the shared dependency');
});

test('a collapsed graph classifies its edges by the real relationship', () => {
    // The bug: groups are drawn as accounts, so both endpoints reported no repository and the
    // classifier read two blanks as a match. Every edge in a collapsed view came out same-repo.
    const nodes = [
        { key: 'octo/app/.github/workflows/ci.yml', kind: 'workflow', repository: 'octo/app' },
        { key: 'octo/lib/.github/workflows/ci.yml', kind: 'workflow', repository: 'octo/lib' },
        { key: 'other/tool/.github/workflows/ci.yml', kind: 'workflow', repository: 'other/tool' },
    ];
    const links = [
        { source: nodes[0].key, target: nodes[1].key, shown: true },
        { source: nodes[0].key, target: nodes[2].key, shown: true },
    ];

    const byRepo = collapseGroups({ groupBy: 'repo' }, nodes, links);
    const cls = Object.fromEntries(byRepo.links.map(l => [`${l.source}>${l.target}`, l.cls]));

    assert.equal(cls['group:octo/app>group:octo/lib'], 'sameOwner',
        'two repositories under one account are a same-owner edge');
    assert.equal(cls['group:other/tool'] ?? cls['group:octo/app>group:other/tool'], 'crossOwner');
    assert.ok(!Object.values(cls).includes('sameRepo'),
        'distinct groups are never the same repository');
});

test('an owner node still has no repository of its own', () => {
    // The guard for the fix above: only an explicit repository may override the node's shape.
    assert.equal(repoOf({ key: 'org:actions', kind: 'org', owner: 'actions' }), '');
});

test('a hidden edge class stays hidden once the graph is folded', () => {
    // The bug: merged edges were created shown, so they never faced the edge filter. Hiding
    // cross-account edges left a starburst of them radiating from the ungrouped bucket.
    const nodes = [
        { key: 'octo/app/.github/workflows/ci.yml', kind: 'workflow', repository: 'octo/app' },
        { key: 'octo/lib/.github/workflows/ci.yml', kind: 'workflow', repository: 'octo/lib' },
        { key: 'other/tool/.github/workflows/ci.yml', kind: 'workflow', repository: 'other/tool' },
    ];
    const links = [
        { source: nodes[0].key, target: nodes[1].key, shown: true },
        { source: nodes[0].key, target: nodes[2].key, shown: true },
    ];
    const it = { groupBy: 'repo', edgeClasses: new Set(['sameRepo', 'sameOwner']) };

    const drawn = collapseGroups(it, nodes, links).links.filter(l => l.shown !== false);
    assert.deepEqual(drawn.map(l => l.cls), ['sameOwner'],
        'the cross-account edge must not come back when folded');
});

// Folding is where "nobody has measured this" turns quietly into "this is zero": a group takes
// the largest reach among its members, and an unmeasured member contributes 0 to that maximum.
// The group has to carry the doubt forward instead of averaging it away.
test('a group holding an unmeasured member does not claim its reach is known', () => {
    const nodes = [
        { key: 'octo/app/a.yml', kind: 'workflow', repository: 'octo/app',
          dependentRepos: 4, reachKnown: true },
        { key: 'octo/app/b.yml', kind: 'workflow', repository: 'octo/app',
          dependentRepos: 0, reachKnown: false },
    ];

    const [group] = collapseGroups({ groupBy: 'repo' }, nodes, []).nodes;

    assert.equal(group.reachKnown, false,
        'one unmeasured member is enough to make the group total a lower bound');
});

test('a group of fully measured members reports its reach as known', () => {
    const nodes = [
        { key: 'octo/app/a.yml', kind: 'workflow', repository: 'octo/app',
          dependentRepos: 4, reachKnown: true },
        { key: 'octo/app/b.yml', kind: 'workflow', repository: 'octo/app',
          dependentRepos: 0, reachKnown: true },
    ];

    const [group] = collapseGroups({ groupBy: 'repo' }, nodes, []).nodes;

    assert.equal(group.reachKnown, true, 'a measured zero is a measurement');
});

// The queue analyzes refs; a node is fully analyzed only when every ref in use has results.
test('a dependency is only analyzed when every reference in use has been', () => {
    assert.equal(fullyAnalyzed({ analyzed: true, refsInUse: 27, refsAnalyzed: 1 }), false,
        'one reference of twenty-seven is not coverage');
    assert.equal(fullyAnalyzed({ analyzed: true, refsInUse: 27, refsAnalyzed: 27 }), true);
});

// Nothing has looked at it, so it cannot be complete. Zero of zero is the absent measurement this
// repository refuses to draw as a zero.
test('a dependency nothing has queued is not reported as fully analyzed', () => {
    assert.equal(fullyAnalyzed({ analyzed: false, refsInUse: 0, refsAnalyzed: 0 }), false);
});

// A node from a server too old to send the counts still has to be classifiable, and its one
// boolean is the best answer available.
test('a node without reference counts falls back to the boolean', () => {
    assert.equal(fullyAnalyzed({ analyzed: true }), true);
    assert.equal(fullyAnalyzed({ analyzed: false }), false);
});

// Coverage is a first-class claim, and a fold is exactly where it gets lost: every group was
// built `analyzed: true`, so a group of repositories nothing had looked inside was drawn as
// though all of it had been.
test('a group holding an unanalyzed member is not reported as analyzed', () => {
    const nodes = [
        { key: 'octo/app/a.yml', kind: 'workflow', repository: 'octo/app', analyzed: true },
        { key: 'octo/app/b.yml', kind: 'workflow', repository: 'octo/app', analyzed: false },
    ];

    const [group] = collapseGroups({ groupBy: 'repo' }, nodes, []).nodes;

    assert.equal(group.analyzed, false,
        'a group is only analyzed when everything folded into it is');
});

// A fold is where coverage gets lost twice: once in the boolean, once in the counts. A group of
// members each partially analyzed is partially analyzed, and the filter has to reach it.
test('a group folds its reference coverage rather than only its boolean', () => {
    const nodes = [
        { key: 'a/one', kind: 'action', repository: '', owner: 'a', analyzed: true, refsInUse: 20, refsAnalyzed: 1 },
        { key: 'a/two', kind: 'action', repository: '', owner: 'a', analyzed: true, refsInUse: 5, refsAnalyzed: 5 },
    ];

    const [group] = collapseGroups({ groupBy: 'owner' }, nodes, []).nodes;

    assert.equal(group.refsInUse, 25);
    assert.equal(group.refsAnalyzed, 6);
    assert.equal(fullyAnalyzed(group), false,
        'a group is only covered when every reference folded into it is');
});

// Two labelling systems ran at once: force-graph's HTML tooltip and this view's canvas labels.
// They drew the same node twice, and once anything was selected the tooltip stayed on whichever
// node it last saw, because it knows nothing about our selection. The canvas label is the only
// one now, so it has to carry the reach the tooltip used to add.
test('a label names the node and its reach in one string', () => {
    assert.equal(labelFor({ key: 'actions/checkout@v4', dependentRepos: 77 }),
        'actions/checkout@v4 \u00b7 77 repositories');
    assert.equal(labelFor({ key: 'actions/checkout@v4', dependentRepos: 0 }),
        'actions/checkout@v4', 'nothing depending on it is not a count worth printing');
});

// A count and the noun beside it have to agree. "1 repos" is the interface reporting a number it
// did not read, on the label a reader meets most often.
test('a count of one takes a singular noun', () => {
    assert.equal(labelFor({ key: 'actions/checkout@v4', dependentRepos: 1 }),
        'actions/checkout@v4 \u00b7 1 repository');
});

// A version labels itself, because the key is already `name@ref` once the queue analyzes per
// reference. Nothing has to re-derive the version from the node's other fields.
test('a per-reference node labels itself with its reference', () => {
    assert.match(labelFor({ key: 'actions/checkout@v3' }), /@v3$/);
});

// The whole reason the default view rendered at 3.4 frames per second: hidden nodes stayed in// the simulation and `nodeVisibility` only stopped them being drawn, so d3-force integrated all
// 9,811 of them every tick. What is not on screen must not be in the simulation.
test('the simulation is given only the nodes that are visible', () => {
    const it = {
        visible: new Set(['a']),
        all: {
            nodes: [{ key: 'a' }, { key: 'b' }, { key: 'c' }],
            links: [
                { source: 'a', target: 'b', shown: false },
                { source: 'a', target: 'a', shown: true },
            ],
        },
    };

    const { nodes, links } = simulationSet(it);

    assert.deepEqual(nodes.map(n => n.key), ['a'], 'hidden nodes are left out entirely');
    assert.equal(links.length, 1, 'only edges the filter kept are simulated');
});

// A filter that changes nothing must not pay for a reseed, or every keystroke reshuffles a
// layout the reader is already using.
test('an unchanged visible set is repainted rather than rebuilt', () => {
    const it = { seeded: true, collapsed: false, visibleSig: 7, renderedSig: 7 };

    assert.equal(needsRender(it), false);
    assert.equal(needsRender({ ...it, visibleSig: 8 }), true,
        'a different visible set has to be handed to the simulation again');
    assert.equal(needsRender({ ...it, seeded: false }), true, 'a first draw always renders');
});

// Two filters can select the same number of nodes and not the same nodes. Comparing counts
// would leave the simulation holding the previous selection.
test('two different selections of the same size are not treated as the same view', () => {
    assert.notEqual(visibleSignature(new Set(['a', 'b'])), visibleSignature(new Set(['a', 'c'])));
    assert.equal(visibleSignature(new Set(['a', 'b'])), visibleSignature(new Set(['b', 'a'])),
        'order is not part of the identity of a set');
});

// Once the layout has settled nothing on screen moves, yet the label pass re-projected and
// redrew an identical frame on every animation frame, for ever. Idle has to cost nothing.
test('a view that has settled with nothing animating stops drawing entirely', () => {
    const settled = { settledAt: 1000, dirty: false, animate: true, selected: new Set() };

    assert.equal(restLevel(settled, 1000 + REST_DELAY), 'still',
        'no selection means no particles, so there is nothing left to move');
});

test('a layout that is still moving keeps drawing', () => {
    const moving = { settledAt: 0, dirty: false, animate: false, selected: new Set() };
    assert.equal(restLevel(moving, 9e9), 'none', 'nothing has settled yet');
    assert.equal(restLevel({ ...moving, settledAt: 1000 }, 1200), 'none',
        'settled a moment ago, still inside the grace period');
});

// Following an expansion is exactly when the screen must not go to sleep: arrivals are the
// reason the reader left it open.
test('a view with arrivals waiting does not rest', () => {
    const arriving = { settledAt: 1000, dirty: true, animate: false, selected: new Set() };
    assert.equal(restLevel(arriving, 9e9), 'none');
});

// Particles mark which way a dependency points, which is worth keeping even on a view nobody is
// touching. At rest they advance in visible steps rather than smoothly, which costs a fraction
// of the frames and still reads as direction.
test('a selection keeps its particles flowing after the view comes to rest', () => {
    const selected = { settledAt: 1000, dirty: false, animate: true, selected: new Set(['a']) };

    assert.equal(restLevel(selected, 9e9), 'idle');
    assert.ok(REST_FPS > 0 && REST_FPS < 30, 'resting still draws, just far less often');
});

test('the ungrouped bucket is named after the grouping that left it out', () => {
    // It read "(not in a workflow)" even when grouping by repository, which describes nothing.
    assert.equal(ungroupedName('repo'), '(not in a repository)');
    assert.equal(ungroupedName('owner'), '(no account)');
    assert.equal(ungroupedName('workflow'), '(not in a workflow)');
});

// A running expansion adds to the corpus continuously. Merging has to leave the picture the
// reader is holding intact, which means keeping every position that already settled.
test('merging keeps the positions of nodes that are already placed', () => {
    const all = {
        nodes: [{ key: 'octo/app', x: 10, y: 20, z: 0, refStyles: new Set(), edgeClasses: new Set() }],
        links: [],
    };

    mergeInto(all, { nodes: [{ key: 'actions/checkout' }], edges: [], metrics: [] });

    const existing = all.nodes.find(n => n.key === 'octo/app');
    assert.deepEqual([existing.x, existing.y], [10, 20]);
});

test('an arrival is placed among the neighbors it arrived attached to', () => {
    const all = {
        nodes: [
            { key: 'a', x: 100, y: 100, z: 0, refStyles: new Set(), edgeClasses: new Set() },
            { key: 'b', x: 140, y: 100, z: 0, refStyles: new Set(), edgeClasses: new Set() },
        ],
        links: [],
    };

    mergeInto(all, {
        nodes: [{ key: 'new' }],
        edges: [{ source: 'a', target: 'new', refs: [], count: 1 },
                { source: 'b', target: 'new', refs: [], count: 1 }],
        metrics: [],
    });

    const arrival = all.nodes.find(n => n.key === 'new');
    // Near the midpoint of its two neighbors, give or take the scatter that stops arrivals
    // stacking on one point.
    assert.ok(Math.abs(arrival.x - 120) < 40, `x was ${arrival.x}`);
    assert.ok(Math.abs(arrival.y - 100) < 40, `y was ${arrival.y}`);
});

test('an arrival with nowhere to attach is left for the renderer to seed', () => {
    const all = { nodes: [], links: [] };
    mergeInto(all, { nodes: [{ key: 'orphan' }], edges: [], metrics: [] });
    assert.equal(all.nodes[0].x, undefined);
});

test('placing arrivals is deterministic, so a reload lands identically', () => {
    const build = () => ({
        nodes: [{ key: 'a', x: 0, y: 0, z: 0 }, { key: 'new' }],
        links: [{ source: 'a', target: 'new' }],
    });
    const first = build(), second = build();
    placeArrivals(first, [first.nodes[1]]);
    placeArrivals(second, [second.nodes[1]]);
    assert.deepEqual(
        [first.nodes[1].x, first.nodes[1].y], [second.nodes[1].x, second.nodes[1].y]);
});

// Seeing the same relationship again means more references, not a second edge between the same
// pair, which would double its weight in the layout.
test('a relationship seen again updates rather than duplicating', () => {
    const all = {
        nodes: [
            { key: 'a', x: 0, y: 0, z: 0, refStyles: new Set(), edgeClasses: new Set() },
            { key: 'b', x: 1, y: 1, z: 0, refStyles: new Set(), edgeClasses: new Set() },
        ],
        links: [{ source: 'a', target: 'b', refs: ['v1'], count: 1 }],
    };

    const added = mergeInto(all, {
        nodes: [],
        edges: [{ source: 'a', target: 'b', refs: ['v1', 'v2'], count: 2 }],
        metrics: [],
    });

    assert.equal(all.links.length, 1);
    assert.equal(added.edges, 0);
    assert.deepEqual(all.links[0].refs, ['v1', 'v2']);
});

// A delta sends only what arrived, so what a lock selects for the relationships behind a line
// accumulates like their references do. Replacing it would forget the commits already known.
test('a relationship seen again keeps every commit a lock selected for it', () => {
    const all = {
        nodes: [
            { key: 'a', x: 0, y: 0, z: 0, refStyles: new Set(), edgeClasses: new Set() },
            { key: 'b', x: 1, y: 1, z: 0, refStyles: new Set(), edgeClasses: new Set() },
        ],
        links: [{ source: 'a', target: 'b', refs: ['v1'], keys: ['k1'], locked: ['c1'], count: 1 }],
    };

    mergeInto(all, {
        nodes: [],
        edges: [{ source: 'a', target: 'b', refs: ['v1'], keys: ['k2'], locked: ['c2'], count: 1 }],
        metrics: [],
    });

    assert.deepEqual(all.links[0].locked, ['c1', 'c2']);
});

// Reach is recomputed wholesale, so a node the client already holds can change size without
// changing in any other way.
test('metrics update nodes the client already holds', () => {
    const all = {
        nodes: [{ key: 'actions/checkout', dependentRepos: 3, reachKnown: true,
                  refStyles: new Set(), edgeClasses: new Set() }],
        links: [],
    };

    mergeInto(all, {
        nodes: [], edges: [],
        metrics: [{ key: 'actions/checkout', dependentRepos: 91, reachKnown: true,
                    findings: 2, advisories: 1, analyzed: true }],
    });

    assert.equal(all.nodes[0].dependentRepos, 91);
    assert.equal(all.nodes[0].analyzed, true);
});

// A merge that only repainted would leave every arrival invisible, however correct the filter
// result was.
test('a merge forces the node set to be handed to the renderer again', () => {
    const it = { seeded: true, collapsed: false, dirty: true };
    assert.equal(needsRender(it), true);
});

// Size carries reach, so the smallest circle reads as "nothing depends on this". A node that
// arrived before the next rebuild has no measurement yet, and must not be drawn as if it did.
test('a node whose reach is not yet measured is not drawn as unused', () => {
    const unmeasured = { key: 'new/action', reachKnown: false, dependentRepos: 0 };
    const measuredZero = { key: 'old/action', reachKnown: true, dependentRepos: 0 };

    assert.ok(nodeRadius(unmeasured) > nodeRadius(measuredZero));
});

// "What does this pull in" and "who is waiting on this" are different questions. Walking
// undirected answers neither, and for a queued dependency it silently answers the wrong one:
// it has no outgoing edges at all, so an undirected hop shows only its callers.
const chain = {
    all: {
        links: [
            { source: 'app', target: 'shared' },
            { source: 'shared', target: 'checkout' },
        ],
    },
};

test('following outward finds what a node uses', () => {
    const seen = neighborhood(chain, 'app', 0, 'out');
    assert.deepEqual([...seen.keys()].sort(), ['app', 'checkout', 'shared']);
});

test('following inward finds what uses a node', () => {
    const seen = neighborhood(chain, 'checkout', 0, 'in');
    assert.deepEqual([...seen.keys()].sort(), ['app', 'checkout', 'shared']);
});

test('a dependency nothing has looked inside has nothing downstream to show', () => {
    const seen = neighborhood(chain, 'checkout', 0, 'out');
    assert.deepEqual([...seen.keys()], ['checkout']);
});

test('walking both ways is not the same as walking one', () => {
    assert.equal(neighborhood(chain, 'shared', 1, 'both').size, 3);
    assert.equal(neighborhood(chain, 'shared', 1, 'out').size, 2);
});

// Most of the corpus was never queued: those nodes are workflows and steps under review, not
// dependencies of it. "Not a dependency" is a fact, and must not be drawn in the same family as
// "we have not looked yet".
test('a node that was never queued is not colored as a discovery state', () => {
    const palette = { muted: '#555', discovery: { queued: '#fc0', done: '#0c8' } };

    assert.equal(discoveryColor({ key: 'octo/app', discovery: '' }, palette), '#555');
    assert.equal(discoveryColor({ key: 'a/b', discovery: 'queued' }, palette), '#fc0');
    assert.equal(discoveryColor({ key: 'c/d', discovery: 'done' }, palette), '#0c8');
});

// The only way out of a selection used to be a button and a chip's close icon, both of which
// have to be found before they can be used.
test('escape leaves the selection when nothing else is claiming the key', () => {
    assert.equal(escapesGraph('Escape', { tagName: 'CANVAS' }, false), true);
    assert.equal(escapesGraph('Escape', { tagName: 'BODY' }, false), true);
    assert.equal(escapesGraph('Escape', null, false), true);
});

test('a key that is not escape is left alone', () => {
    assert.equal(escapesGraph('Enter', { tagName: 'CANVAS' }, false), false);
    assert.equal(escapesGraph('a', null, false), false);
    assert.equal(escapesGraph('Esc', { tagName: 'CANVAS' }, false), false);
});

// Escape in a search box means "clear what I typed". Taking it to also drop the selection would
// throw away the reader's whole context as a side effect of abandoning one word.
test('escape while typing belongs to the field, not the graph', () => {
    assert.equal(escapesGraph('Escape', { tagName: 'INPUT' }, false), false);
    assert.equal(escapesGraph('Escape', { tagName: 'TEXTAREA' }, false), false);
    assert.equal(escapesGraph('Escape', { tagName: 'SELECT' }, false), false);
    assert.equal(escapesGraph('Escape', { tagName: 'DIV', isContentEditable: true }, false), false);
});

// An open menu answers Escape by closing itself. If the graph answered the same press, one key
// would undo two things and the reader could only see one of them.
test('escape closes an open menu before it reaches the graph', () => {
    assert.equal(escapesGraph('Escape', { tagName: 'CANVAS' }, true), false);
    assert.equal(escapesGraph('Escape', null, true), false);
});

test('a delta that removes a node takes it out of the graph', () => {
    const all = {
        nodes: [{ key: 'a', refStyles: new Set(), edgeClasses: new Set() },
                { key: 'b', refStyles: new Set(), edgeClasses: new Set() }],
        links: [{ source: 'a', target: 'b', refs: [], count: 1 }],
    };

    mergeInto(all, { nodes: [], edges: [], removedNodes: ['b'], removedEdges: [] });

    assert.deepEqual(all.nodes.map(n => n.key), ['a']);
    // An edge whose end is gone is a break nothing can draw.
    assert.equal(all.links.length, 0);
});

test('a delta that removes an edge leaves its endpoints alone', () => {
    const all = {
        nodes: [{ key: 'a', refStyles: new Set(), edgeClasses: new Set() },
                { key: 'b', refStyles: new Set(), edgeClasses: new Set() }],
        links: [{ keys: ['e1'], source: 'a', target: 'b', refs: [], count: 1 }],
    };

    mergeInto(all, { nodes: [], edges: [], removedNodes: [], removedEdges: ['e1'] });

    assert.equal(all.links.length, 0);
    assert.deepEqual(all.nodes.map(n => n.key), ['a', 'b']);
});

// The test above hands `mergeInto` a link carrying `keys`, which is what the wire carries: a line
// stands for every relationship between its endpoints, and the delta names removals by the same
// `edge_key`. For a long time the server sent no key at all — `GraphEdge` serialized source,
// target, kind, boundary, refs and count — so `e.key` was undefined for every real edge and
// `removedEdges` matched nothing. Removal was a silent no-op against the only payload that
// actually exists, and a fixture inventing a field was the reason nobody saw it.
test('an edge from the real payload can be removed by the key the delta names', () => {
    const fromServer = {
        keys: ['octo/app:.github/workflows/ci.yml:8:15'],
        source: 'a', target: 'b', kind: 'step-uses-action', refs: ['v4'], count: 1,
    };
    const all = {
        nodes: [{ key: 'a', refStyles: new Set(), edgeClasses: new Set() },
                { key: 'b', refStyles: new Set(), edgeClasses: new Set() }],
        links: [fromServer],
    };

    mergeInto(all, {
        nodes: [], edges: [], removedNodes: [],
        removedEdges: ['octo/app:.github/workflows/ci.yml:8:15'],
    });

    assert.equal(all.links.length, 0, 'the delta named this edge and it is still drawn');
});

// Two steps using one action are two relationships and one line. Taking the line away because one
// of them was removed would hide a relationship that is still declared.
test('a delta removes the relationship it names and leaves the rest of the line', () => {
    const all = {
        nodes: [{ key: 'a', refStyles: new Set(), edgeClasses: new Set() },
                { key: 'b', refStyles: new Set(), edgeClasses: new Set() }],
        links: [{ keys: ['e1', 'e2'], source: 'a', target: 'b', refs: ['v4'], count: 2 }],
    };

    mergeInto(all, { nodes: [], edges: [], removedNodes: [], removedEdges: ['e1'] });

    assert.equal(all.links.length, 1, 'e2 is still declared and the line is gone');
    assert.deepEqual(all.links[0].keys, ['e2']);
    assert.equal(all.links[0].count, 1, 'the line still claims two relationships');
});

// A delta carries the rows that arrived, not the pair's whole history, so a second relationship
// between an already drawn pair arrives alone. Overwriting would forget the first one, and its
// later removal would match nothing — the silent no-op again, one step further along.
test('a pair sent again names the union of its relationships', () => {
    const all = {
        nodes: [{ key: 'a', refStyles: new Set(), edgeClasses: new Set() },
                { key: 'b', refStyles: new Set(), edgeClasses: new Set() }],
        links: [{ keys: ['e1'], source: 'a', target: 'b', kind: 'step-uses-action',
                  refs: ['v4'], count: 1, refStyle: 'tag' }],
    };

    mergeInto(all, {
        nodes: [], removedNodes: [], removedEdges: [], metrics: [],
        edges: [{ keys: ['e2'], source: 'a', target: 'b', kind: 'step-uses-action',
                  refs: ['v3'], count: 1, refStyle: 'tag' }],
    });

    assert.equal(all.links.length, 1, 'the same pair is one line');
    assert.deepEqual(all.links[0].keys, ['e1', 'e2']);
    assert.equal(all.links[0].count, 2);
    assert.deepEqual(all.links[0].refs, ['v4', 'v3'], 'a reference still in use was dropped');
});

// The server reports a fact as shared or as mixed, and merging has to reach the same answer: a
// line whose arriving relationship is pinned and whose drawn one is not is mixed, and keeping
// either value would state as the pair's what is true of one of them.
test('a fact the merged relationships disagree about becomes mixed', () => {
    const all = {
        nodes: [{ key: 'a', refStyles: new Set(), edgeClasses: new Set() },
                { key: 'b', refStyles: new Set(), edgeClasses: new Set() }],
        links: [{ keys: ['e1'], source: 'a', target: 'b', kind: 'step-uses-action',
                  refs: ['v4'], count: 1, refStyle: 'tag', boundary: 'cross-owner' }],
    };

    mergeInto(all, {
        nodes: [], removedNodes: [], removedEdges: [], metrics: [],
        edges: [{ keys: ['e2'], source: 'a', target: 'b', kind: 'step-uses-action',
                  refs: ['abc'], count: 1, refStyle: 'commit', boundary: 'cross-owner' }],
    });

    const line = all.links[0];
    assert.ok((line.mixed || []).includes('refStyle'), 'one is pinned and one is not');
    assert.ok(!line.refStyle, 'a disagreed fact is still reported as a value');
    assert.equal(line.boundary, 'cross-owner', 'a shared fact was thrown away');
    assert.ok(!(line.mixed || []).includes('boundary'));
});

// A rested view has paused its renderer, so a mutator that changes what a frame shows and does
// not wake it changes the state and repaints nothing. Switching the Priority force did exactly
// that: the caption and the button changed, the layout stayed on the previous force, and the
// interface told the reader position meant urgency while showing them structure.
//
// Structural rather than behavioral, because `instances` is module-private and the bug is about
// a call that is missing. It also catches the next mutator somebody adds.
test('every mutator that changes what a frame shows wakes the view', () => {
    const source = readFileSync(new URL('./graph.js', import.meta.url), 'utf8');
    const mutators = [
        'setView', 'setGroupBy', 'setPalette', 'setSelection',
        'setAnimate', 'setCollapsed', 'setMode',
    ];

    const missing = mutators.filter(name => {
        const start = source.indexOf(`export function ${name}(`);
        assert.ok(start >= 0, `${name} is gone; update this guard`);
        const next = source.indexOf('\nexport function ', start + 1);
        const body = source.slice(start, next < 0 ? source.length : next);
        // applyFilters wakes on the way through, so delegating to it counts.
        return !/\bwake\(/.test(body) && !/\bapplyFilters\(/.test(body);
    });

    assert.deepEqual(missing, [],
        'these change what a frame shows without waking the view, so a rested renderer never repaints');
});

// Reader gestures must wake a rested renderer before moving the camera.
test('a view that has come to rest is woken by the reader touching it', () => {
    const source = readFileSync(new URL('./graph.js', import.meta.url), 'utf8');
    const start = source.indexOf('export function create(');
    assert.ok(start >= 0, 'create is gone; update this guard');
    const body = source.slice(start, source.indexOf('\nexport function ', start + 1));

    for (const gesture of ['pointerdown', 'wheel']) {
        const bound = new RegExp(`addEventListener\\('${gesture}'`).test(body);
        assert.ok(bound, `create() never listens for ${gesture}, so a rested view stays frozen`);
    }
    assert.ok(/wake\(/.test(body), 'create() binds gestures but none of them wakes the view');
});

// The scale the Priority force divides by. Recomputed when priorities arrive, because the
// rendered node set does not change on a view switch and the layout would otherwise be laid out
// against whatever the last rebuild happened to leave behind.
test('priorities set the scale the priority force divides by', () => {
    const source = readFileSync(new URL('./graph.js', import.meta.url), 'utf8');
    const start = source.indexOf('export function setPriorities(');
    const next = source.indexOf('\nexport function ', start + 1);
    const body = source.slice(start, next < 0 ? source.length : next);

    assert.match(body, /maxPriority/,
        'setPriorities must refresh maxPriority, or the force divides by a stale scale');
});

// `getBoundingClientRect().top` is already viewport-relative; scrollY must not shrink the graph.
test('the graph fills the window below itself, wherever the page is scrolled', () => {
    // Unscrolled: the host sits 200px down a 1000px window, so it takes the rest bar the gutter.
    assert.equal(graphHeight({ top: 200, innerHeight: 1000, gutter: 64 }), 736);

    // Scrolled far enough that the host's top has left the top of the window. It has not become
    // smaller — there is more room below it, not less — so it takes the whole window bar the
    // gutter.
    assert.equal(graphHeight({ top: -400, innerHeight: 1000, gutter: 64 }), 936);
});

test('a host with no room below it still gets a usable floor', () => {
    // Pushed near the bottom of the window there is genuinely nothing to give it, and a graph
    // 36px tall is not a graph. The floor is what a reader can still read.
    assert.equal(graphHeight({ top: 900, innerHeight: 1000, gutter: 64 }), 360);
});

// Refit only when scrolling materially changes the available canvas height.
test('a refit is worth paying for only when the room has materially changed', () => {
    // Resizing a WebGL canvas reallocates its drawing buffer. Doing that for a few pixels of
    // scroll costs frames and flickers, so small differences are left alone.
    assert.equal(refitNeeded(536, 540), false);
    assert.equal(refitNeeded(536, 536), false);

    // Growing into the room scrolling revealed, and giving it back on the way up, are both worth
    // a resize: this is the difference between the floor and half as much graph again.
    assert.equal(refitNeeded(360, 536), true);
    assert.equal(refitNeeded(536, 360), true);
});

// The view was mute: text was drawn for the selection and the hovered node and nothing else, so a
// reader could not name anything on screen without clicking it blindly. These decide who gets it.
test('the widest-reaching nodes carry a label without being asked', () => {
    const nodes = [
        { key: 'a', dependentRepos: 17, reachKnown: true, sx: 10, sy: 10 },
        { key: 'b', dependentRepos: 11, reachKnown: true, sx: 20, sy: 20 },
        { key: 'c', dependentRepos: 1, reachKnown: true, sx: 30, sy: 30 },
    ];
    const tiers = labelTiers(nodes, { top: 2, radius: 0 });

    assert.equal(tiers.get('a'), 'name');
    assert.equal(tiers.get('b'), 'name');
    // Not a judgement about c, a rendering budget. Reach is the ranking that already sizes a node,
    // so the ones that get text are the ones already drawn largest.
    assert.equal(tiers.has('c'), false);
});

test('a node whose reach was never measured cannot win a slot it did not earn', () => {
    const nodes = [
        { key: 'measured', dependentRepos: 3, reachKnown: true, sx: 0, sy: 0 },
        { key: 'unmeasured', dependentRepos: 0, reachKnown: false, sx: 5, sy: 5 },
    ];
    const tiers = labelTiers(nodes, { top: 1, radius: 0 });

    assert.equal(tiers.get('measured'), 'name');
    assert.equal(tiers.has('unmeasured'), false);
});

test('everything near the pointer is named, which is how a dense patch becomes readable', () => {
    const nodes = [
        { key: 'near', dependentRepos: 0, sx: 100, sy: 100 },
        { key: 'alsoNear', dependentRepos: 0, sx: 140, sy: 120 },
        { key: 'far', dependentRepos: 0, sx: 400, sy: 400 },
    ];
    const tiers = labelTiers(nodes, { top: 0, radius: 60, pointer: { x: 110, y: 110 } });

    assert.equal(tiers.get('near'), 'name');
    assert.equal(tiers.get('alsoNear'), 'name');
    assert.equal(tiers.has('far'), false);
});

test('the node under the pointer and the selection get their details, not just a name', () => {
    const nodes = [
        { key: 'hovered', dependentRepos: 0, sx: 0, sy: 0 },
        { key: 'picked', dependentRepos: 0, sx: 300, sy: 300 },
        { key: 'big', dependentRepos: 99, reachKnown: true, sx: 500, sy: 500 },
    ];
    const tiers = labelTiers(nodes, {
        top: 1, radius: 0, hovered: 'hovered', selected: new Set(['picked']),
    });

    assert.equal(tiers.get('hovered'), 'detail');
    assert.equal(tiers.get('picked'), 'detail');
    // The widest-reaching node still gets its name; detail outranks a name where they collide.
    assert.equal(tiers.get('big'), 'name');
});

// Naming a node is half of it. A reader also has to know which repository and workflow it belongs
// to, which is the question this view exists to answer.
test('a node says where it lives, without inventing a measurement it does not have', () => {
    const workflow = nodeBrief({
        kind: 'workflow', repository: 'octo/app', path: '.github/workflows/ci.yml',
        dependentRepos: 4, reachKnown: true,
    });
    assert.equal(workflow.repository, 'octo/app');
    assert.equal(workflow.dependentRepos, 4);
    assert.equal(workflow.reachKnown, true);

    // Absent is not zero: a node ingested since the last reach rebuild has no measurement, and
    // printing "0 repositories" would assert one nobody made.
    const fresh = nodeBrief({ kind: 'action', owner: 'actions', reachKnown: false, dependentRepos: 0 });
    assert.equal(fresh.reachKnown, false);
});

// The halo names everything near the pointer, which is how a dense patch becomes readable. It is
// also what made it hard to hover one node in particular: six boxes of a repeated path prefix
// covered the very targets a reader was aiming at.
test('the halo gets out of the way once the reader has landed on something', () => {
    const nodes = [
        { key: 'target', dependentRepos: 0, sx: 100, sy: 100 },
        { key: 'neighbor', dependentRepos: 0, sx: 120, sy: 110 },
        { key: 'another', dependentRepos: 0, sx: 90, sy: 130 },
    ];
    const scanning = labelTiers(nodes, { radius: 80, top: 0, pointer: { x: 105, y: 108 } });
    assert.equal(scanning.size, 3, 'scanning should name the whole patch');

    // Landed. Only the node under the pointer, so nothing hides what is next to it.
    const landed = labelTiers(nodes, {
        radius: 80, top: 0, pointer: { x: 105, y: 108 }, hovered: 'target',
    });
    assert.equal(landed.get('target'), 'detail');
    assert.equal(landed.has('neighbor'), false, 'the halo did not yield to the hover');
    assert.equal(landed.has('another'), false);
});

test('a selection is not the halo, and survives a hover elsewhere', () => {
    const nodes = [
        { key: 'picked', dependentRepos: 0, sx: 500, sy: 500 },
        { key: 'target', dependentRepos: 0, sx: 100, sy: 100 },
    ];
    const tiers = labelTiers(nodes, {
        radius: 80, top: 0, pointer: { x: 100, y: 100 },
        hovered: 'target', selected: new Set(['picked']),
    });

    assert.equal(tiers.get('picked'), 'detail', 'a chosen node stopped being labelled');
    assert.equal(tiers.get('target'), 'detail');
});

// A selection is not always a list of choices. One dependency analyzed at 35 references is one
// thing the reader asked about, drawn as 35 nodes, and every one of them claimed a detail label —
// a stack of boxes over the middle of the graph naming versions nobody picked. The chip above the
// canvas has counted rather than listed since it was written; the canvas had no such cap.
test('a selection too large to name is labelled where it reaches furthest', () => {
    const nodes = [
        { key: 'dep@v1', dependentRepos: 18, sx: 100, sy: 100 },
        { key: 'dep@v2', dependentRepos: 8, sx: 110, sy: 100 },
        { key: 'dep@v3', dependentRepos: 2, sx: 120, sy: 100 },
        { key: 'dep@v4', dependentRepos: 1, sx: 130, sy: 100 },
    ];
    const selected = new Set(['dep@v1', 'dep@v2', 'dep@v3', 'dep@v4']);

    const tiers = labelTiers(nodes, { top: 0, radius: 0, selected, selectedMax: 2 });

    assert.equal(tiers.get('dep@v1'), 'detail');
    assert.equal(tiers.get('dep@v2'), 'detail');
    assert.equal(tiers.has('dep@v3'), false, 'every selected node still claimed a label');
    assert.equal(tiers.has('dep@v4'), false);
});

test('one chosen node is always named, however tight the budget', () => {
    const nodes = [{ key: 'picked', dependentRepos: 0, sx: 100, sy: 100 }];

    const tiers = labelTiers(nodes, { top: 0, radius: 0, selected: new Set(['picked']), selectedMax: 2 });

    assert.equal(tiers.get('picked'), 'detail');
});

test('the node under the pointer is named even when the selection filled the budget', () => {
    // Otherwise pointing at a node inside a large selection stops answering, which is the one
    // gesture that must always work.
    const nodes = [
        { key: 'dep@v1', dependentRepos: 18, sx: 100, sy: 100 },
        { key: 'dep@v2', dependentRepos: 8, sx: 110, sy: 100 },
        { key: 'dep@v3', dependentRepos: 2, sx: 120, sy: 100 },
    ];
    const selected = new Set(['dep@v1', 'dep@v2', 'dep@v3']);

    const tiers = labelTiers(nodes, { top: 0, radius: 0, selected, selectedMax: 2, hovered: 'dep@v3' });

    assert.equal(tiers.get('dep@v3'), 'detail');
});

// A label printed the whole key, which is why each box was around 500px wide. Inside a boundary
// that already names the container, the rest of the path is the part a reader does not need.
test('a label drops the context a region already supplies', () => {
    const workflow = { key: 'actions/setup-python/.github/workflows/check-dist.yml' };

    assert.equal(leafLabel(workflow, 'actions/setup-python'), 'check-dist.yml');
    // No region, or one naming something else: nothing else on screen says where this lives, so
    // shortening it would leave a name that identifies nothing.
    assert.equal(leafLabel(workflow, ''), workflow.key);
    assert.equal(leafLabel(workflow, 'some-other-owner'), workflow.key);
});

test('shortening never leaves a node with no name at all', () => {
    // The container and the node are the same thing, which happens for a repository inside its own
    // region. There is no leaf left, so the full key stays.
    assert.equal(leafLabel({ key: 'octo/app' }, 'octo/app'), 'octo/app');
});

// A container whose boundary was refused is drawn as a point again, and its key is synthetic.
// `org:actions` was the label a reader saw for the account `actions`.
test('a container put back on screen names its account, not its key', () => {
    assert.equal(leafLabel({ key: 'org:actions', kind: 'org', owner: 'actions' }, ''), 'actions');
    assert.equal(leafLabel({ key: 'org:super-linter', kind: 'org' }, ''), 'super-linter');
});

// A label centered near the canvas edge must be clamped back inside it.
test('a label at the edge is kept inside the canvas', () => {
    assert.equal(clampToCanvas({ x: -20, w: 100 }, 600), 4);
    assert.equal(clampToCanvas({ x: 560, w: 100 }, 600), 496);
    assert.equal(clampToCanvas({ x: 100, w: 100 }, 600), 100);
    // Wider than the canvas: start at the left edge rather than pushing it off the other side.
    assert.equal(clampToCanvas({ x: 10, w: 900 }, 600), 4);
});

// A commit-pinned reference is 40 hex characters of a name that is already long. The panel carries
// the whole key; the canvas needs only enough of it to tell two references apart.
test('a commit sha in a label is abbreviated, and a tag is left alone', () => {
    assert.equal(shortRef('actions/cache@e1a02359b239bd28de3f6d35fdc870250fa513d5'),
        'actions/cache@e1a0235');
    assert.equal(shortRef('actions/cache@v4'), 'actions/cache@v4');
    assert.equal(shortRef('actions/cache@main'), 'actions/cache@main');
    assert.equal(shortRef('actions/cache'), 'actions/cache');
});

test('a label carries the abbreviated reference, not the full sha', () => {
    const n = { key: 'actions/cache@e1a02359b239bd28de3f6d35fdc870250fa513d5', dependentRepos: 0 };
    assert.equal(labelFor(n, ''), 'actions/cache@e1a0235');
});

// The halo made a dense patch readable and made it unclickable: every node within the radius got a
// box, so in the middle of the graph a reader aimed at a node through a column of other nodes'
// names.
test('the halo names the nearest few rather than everything inside its radius', () => {
    const nodes = [];
    for (let i = 0; i < 20; i++) nodes.push({ key: `n${i}`, dependentRepos: 0, sx: 100 + i, sy: 100 });
    const tiers = labelTiers(nodes, { radius: 200, top: 0, haloMax: 4, pointer: { x: 100, y: 100 } });

    assert.equal(tiers.size, 4, [...tiers.keys()].join(','));
    // The nearest, not the first twenty the loop happened to reach.
    assert.deepEqual([...tiers.keys()].sort(), ['n0', 'n1', 'n2', 'n3']);
});

test('a halo label fades with distance, and never to nothing', () => {
    assert.equal(haloAlpha(0, 100), 1);
    assert.ok(haloAlpha(50, 100) < haloAlpha(10, 100));
    // An unlabelled node is a rendering budget, never a judgement — so a label that is drawn at
    // all has to stay readable rather than trail off into the background.
    assert.ok(haloAlpha(100, 100) >= 0.35, haloAlpha(100, 100));
    assert.equal(haloAlpha(400, 100), haloAlpha(100, 100), 'alpha kept falling past the radius');
});

// The hovered node's detail box was the largest thing on screen, and it occluded the neighbors a
// reader hovers a node to compare it against. The canvas keeps a name and where it lives; the rest
// is typeset in the panel, where it costs no pixels over the graph.
test('the canvas keeps one context line and leaves the rest to the panel', () => {
    const n = {
        key: 'octo/app/.github/workflows/ci.yml', kind: 'reusable_workflow', repository: 'octo/app',
        path: '.github/workflows/ci.yml', grouping: 'payments', findings: 3,
        dependentRepos: 4, reachKnown: true,
    };
    const line = nodeContext(n);

    assert.equal(typeof line, 'string');
    assert.ok(!line.includes('\n'), line);
    assert.ok(/reusable workflow/.test(line), line);
    assert.ok(line.includes('octo/app'), line);
    // The panel is where the rest lives, and it has to hold strictly more than the canvas does.
    assert.equal(nodeBrief(n).path, '.github/workflows/ci.yml');
    assert.equal(nodeBrief(n).findings, 3);
});

test('a node whose repository is its own name does not say it twice', () => {
    assert.equal(nodeContext({ key: 'octo/app', kind: 'repo', repository: 'octo/app' }), 'repo');
});

// A boundary drawn around the members that survived a filter implies the container is only that.
// The footer already states the same relationship for the whole view; a region owes it too.
test('a region says how many of its members it is not drawing', () => {
    // At rest the name is the point and the disclosure is compact, because seventeen of these
    // share a 669px canvas and the long form is what made them stack into a column.
    assert.deepEqual(regionLabelParts('github', 46, 311, false), { name: 'github', count: ' 46/311' });
    // Nothing elided, nothing to disclose: a complete boundary already shows all its members.
    assert.deepEqual(regionLabelParts('github', 311, 311, false), { name: 'github', count: '' });

    // Moused over, it is the one the reader is asking about, so it can afford the sentence.
    assert.equal(regionLabelParts('github', 46, 311, true).count, ' \u00b7 46 of 311');
    assert.equal(regionLabelParts('github', 311, 311, true).count, ' \u00b7 311');
});

// Which boundary the reader is asking about. Nested and overlapping boundaries both happen, so
// the smallest one under the pointer is the one being pointed at.
test('the region under the pointer is the smallest one containing it', () => {
    const big = { key: 'big', area: 10000, hull: square(0, 0, 100) };
    const small = { key: 'small', area: 400, hull: square(40, 40, 20) };
    const shapes = [big, small];

    assert.equal(hoveredRegion(shapes, { x: 45, y: 45 }).key, 'small');
    assert.equal(hoveredRegion(shapes, { x: 5, y: 5 }).key, 'big');
    assert.equal(hoveredRegion(shapes, { x: 500, y: 500 }), null);
    assert.equal(hoveredRegion(shapes, null), null);
});

function square(x, y, size) {
    return [{ x, y }, { x: x + size, y }, { x: x + size, y: y + size }, { x, y: y + size }];
}

// The camera framed a sphere while the canvas is 1.86:1, so the graph was cut off the bottom with
// half the width unused: the cloud spanned x 216-516 of 669 and y 46-426 of 360.
test('the camera is placed so the core fits both dimensions of the canvas', () => {
    const fov = 50;
    const tall = fitDistance(100, 300, 1.86, fov);
    const wide = fitDistance(300, 100, 1.86, fov);
    const half = Math.tan((fov * Math.PI / 180) / 2);

    // A tall core is bound by the height and a wide one by the width, which is the distinction a
    // single radius could not make.
    assert.ok(tall > wide, `${tall} against ${wide}`);
    assert.ok(Math.abs(tall * half - 300) < 1, `${tall * half}`);
    assert.ok(Math.abs(wide * half * 1.86 - 300) < 1, `${wide * half * 1.86}`);
});

// A boundary is drawn a fixed 26 screen pixels outside its members however far away the camera is,
// and carries a name above that. Framing the members alone clipped both against the edge.
test('the camera leaves room for the boundary, measured in the pixels it is drawn in', () => {
    // Half of a 360px canvas: 46px of gutter is about a quarter again.
    assert.ok(Math.abs(fitMargin(46, 180) - 1.256) < 0.001, fitMargin(46, 180));
    // A taller canvas needs proportionally less, because the gutter is a fixed number of pixels.
    assert.ok(fitMargin(46, 400) < fitMargin(46, 180));
    assert.equal(fitMargin(46, 0), 1, 'a canvas with no height asks for no margin');
});

test('the core is the dense part, centred on itself rather than on the origin', () => {    const nodes = [];
    for (let i = 0; i < 20; i++) nodes.push({ x: 1000 + i, y: 500 + i, z: 0 });
    // One straggler, which is what framing the full extent would shrink everything else for.
    nodes.push({ x: 9000, y: 9000, z: 0 });

    const core = coreBounds(nodes, 0.9);

    assert.ok(Math.abs(core.cx - 1009) < 12, `${core.cx}`);
    assert.ok(Math.abs(core.cy - 509) < 12, `${core.cy}`);
    assert.ok(core.halfWidth < 100, `the outlier was kept: ${core.halfWidth}`);
});

// A circular cloud in a 1.86:1 canvas leaves the sides empty however well it is framed. Gravity is
// the restoring force, so weakening it across the wide axis is what lets the layout take the shape
// of the room it has.
test('the layout is given the proportions of the canvas it is drawn in', () => {
    const node = { x: 100, y: 100, z: 0, vx: 0, vy: 0, vz: 0 };
    const force = gravityForce(() => 0.1, 2);
    force.initialize([node]);
    force(1);

    assert.ok(Math.abs(node.vx) < Math.abs(node.vy), 'the wide axis was not given more room');
    assert.ok(Math.abs(node.vx * 2 - node.vy) < 1e-9, JSON.stringify(node));
});

// Dividing gravity by the aspect alone moved the cloud from 45% to 52% of the canvas width, because
// an equilibrium extent goes as the cube root of the restoring constant rather than its reciprocal.
test('spreading by the aspect means weakening gravity by rather more than it', () => {
    assert.ok(spreadFor(1.86) > 6, spreadFor(1.86));
    assert.equal(spreadFor(1), 1, 'a square canvas asks for no spread at all');
    assert.equal(spreadFor(0.5), 1, 'a tall canvas is not given a negative one');
    // A pull weak enough to stop mattering is how orphans came to own the bounding box.
    assert.equal(spreadFor(40), 12);
});

// The simulation stops after a fixed number of ticks, and a weakened pull had not finished
// travelling when it did — a reheat took the cloud from 1.05:1 to 1.15:1. Starting at the right
// shape is what makes the short relaxation enough.
test('the layout starts on an ellipse shaped like the canvas', () => {
    const round = seedAt('octo/app', 600, 1, false);
    const wide = seedAt('octo/app', 600, 1.86, false);

    assert.ok(Math.abs(wide.x) > Math.abs(round.x), `${wide.x} against ${round.x}`);
    assert.equal(wide.y, round.y, 'the tall axis was stretched too');
    // A tall canvas gets a circle rather than a squashed one: the room is the constraint, and
    // there is none to spare across the narrow axis.
    assert.equal(seedAt('octo/app', 600, 0.5, false).x, round.x);
});

test('seeding is deterministic, so a reload lays out identically', () => {
    assert.deepEqual(seedAt('octo/app', 600, 1.86, true), seedAt('octo/app', 600, 1.86, true));
    assert.notDeepEqual(seedAt('octo/app', 600, 1.86, true), seedAt('octo/lib', 600, 1.86, true));
    // A z coordinate only means anything in 3D.
    assert.equal(seedAt('octo/app', 600, 1.86, false).z, 0);
});

test('a region name sits inside its own boundary when it fits', () => {
    const hull = inflate(hullOf([
        { sx: 0, sy: 0 }, { sx: 400, sy: 0 }, { sx: 400, sy: 300 }, { sx: 0, sy: 300 },
    ]), 10);
    const spot = regionLabelSpot(hull, 120, 16, [], { width: 800, height: 600 });

    assert.ok(spot, 'a region with room to spare could not place its own name');
    assert.ok(spot.inside, JSON.stringify(spot));
    assert.ok(!spot.leader, 'a name already inside its boundary does not need a line back to it');
    for (const corner of [[-60, -8], [60, -8], [-60, 8], [60, 8]]) {
        assert.ok(containsPoint(hull, { x: spot.x + corner[0], y: spot.y + corner[1] }),
            `corner ${corner} escaped the boundary it names`);
    }
});

test('a name too wide for its region goes outside with a line back to it', () => {
    const hull = inflate(hullOf([
        { sx: 0, sy: 0 }, { sx: 30, sy: 0 }, { sx: 30, sy: 20 }, { sx: 0, sy: 20 },
    ]), 6);
    const spot = regionLabelSpot(hull, 180, 16, [], { width: 800, height: 600 });

    assert.ok(spot);
    assert.equal(spot.inside, false);
    assert.ok(spot.leader, 'a name outside its boundary is a name whose region is unknown');
});

// They used to be dropped outright on a collision, so in the crowded middle the regions that most
// needed naming were the ones that went unnamed.
// A hue tuned to read on near-black washes out to nothing on white, which is where the region
// names went in the light scheme. The palette carries no flag, and the answer is a property of the
// background color anyway.
test('the scheme in force is read from the background it paints on', () => {
    assert.equal(isDarkBackground('#1a1b1e'), true);
    assert.equal(isDarkBackground('#ffffff'), false);
    assert.equal(isDarkBackground('rgb(26, 27, 30)'), true);
    assert.equal(isDarkBackground('rgb(255, 255, 255)'), false);
    // Nothing to read: dark is the safer guess, because a light stroke on an unknown surface is
    // the one that can disappear entirely.
    assert.equal(isDarkBackground(''), true);
});

// Rotation is meaningless on a plane and is switched off there, which left the button a reader
// drags first doing nothing at all — while the canvas hint said "Drag: pan".
test('a flat view pans with the button a reader actually drags', () => {
    const trackball = { LEFT: 0, MIDDLE: 1, RIGHT: 2 };

    assert.deepEqual(buttonMap(trackball, false), { LEFT: 2, MIDDLE: 1, RIGHT: 0 });
    assert.deepEqual(buttonMap(trackball, true), trackball, 'a 3D view still rotates on the left');
});

// A node is drawn at half a pixel, so the renderer's own hit test almost never fires and the panel
// read as stuck on whichever node it last managed to catch. Screen distance is the question a
// reader is actually asking, and the label pass has already projected every node to answer it.
test('the node a pointer is asking about is the nearest one to it', () => {
    const points = [
        { key: 'far', sx: 200, sy: 200 },
        { key: 'near', sx: 104, sy: 103 },
        { key: 'nearer', sx: 101, sy: 101 },
    ];

    assert.equal(nearestTo(points, { x: 100, y: 100 }, 20).key, 'nearer');
    assert.equal(nearestTo(points, { x: 500, y: 500 }, 20), null, 'nothing is near enough to ask about');
    assert.equal(nearestTo(points, null, 20), null);
});

test('two nodes the same distance away resolve the same way every frame', () => {
    const points = [
        { key: 'b', sx: 110, sy: 100 },
        { key: 'a', sx: 90, sy: 100 },
    ];

    assert.equal(nearestTo(points, { x: 100, y: 100 }, 20).key, 'a');
    assert.equal(nearestTo([...points].reverse(), { x: 100, y: 100 }, 20).key, 'a');
});

// An edge needs both ends; outward dependency context answers what shown nodes use.
test('a shown node keeps what it uses, so a relationship has both of its ends', () => {
    const visible = new Set(['octo/app']);
    const links = [
        { source: 'octo/app', target: 'actions/checkout', cls: 'crossOwner' },
        // Inbound: a different question, and the one worth an eighth as much.
        { source: 'other/uses-it', target: 'octo/app', cls: 'crossOwner' },
    ];

    const context = withDependencies(visible, links, new Set(['crossOwner']));

    assert.deepEqual([...context], ['actions/checkout']);
});

test('an edge class the reader switched off brings nothing back with it', () => {
    const visible = new Set(['octo/app']);
    const links = [{ source: 'octo/app', target: 'actions/checkout', cls: 'crossOwner' }];

    assert.equal(withDependencies(visible, links, new Set(['sameRepo'])).size, 0);
});

test('nothing is added for a relationship that already has both ends', () => {
    const visible = new Set(['octo/app', 'actions/checkout']);
    const links = [{ source: 'octo/app', target: 'actions/checkout', cls: 'crossOwner' }];

    assert.equal(withDependencies(visible, links, new Set(['crossOwner'])).size, 0);
});

// Naming a node suspends arrival filters that would hide its neighborhood.
test('naming a node is itself a scope, so the arrival filters stop deciding', () => {
    const asked = { hasFindings: true, hasAdvisory: true, unanalyzedOnly: true, minReach: 5 };

    assert.deepEqual(scopeFilters({ ...asked }), { ...asked, removes: true },
        'the arrival scope still filters');
    assert.deepEqual(scopeFilters({ ...asked, selection: 'octo/app' }),
        { hasFindings: false, hasAdvisory: false, unanalyzedOnly: false, minReach: 0, removes: false });
});

// Boundaries and relationships cannot both survive a filter. A boundary drawn around only the
// nodes that matched claims the group is *only* those, so grouping has to remove what it filters --
// and an edge needs both its ends, so removing takes the relationships too. Ungrouped there is no
// such claim to keep honest, and nothing has to go.
test('grouping decides whether a filter removes what it did not match, or greys it', () => {
    assert.equal(scopeFilters({ hasFindings: true, grouped: true }).removes, true);
    assert.equal(scopeFilters({ hasFindings: true, grouped: false }).removes, false);
    // A named node suspends the questions outright, so there is nothing left for either to do.
    assert.equal(scopeFilters({ hasFindings: true, grouped: false, selection: 'octo/app' }).removes, false);
    assert.equal(scopeFilters({ hasFindings: true, grouped: true, selection: 'octo/app' }).removes, false);
});

// Removing what a scope question does not match takes the relationships with it, because an edge
// needs both ends: `has findings` on by default left 39 of 7,552 edges, so almost nothing could be
// followed anywhere. The question still gets an answer; what fails it recedes instead of leaving.
test('a node that fails a scope question is still drawn, as context rather than an answer', () => {
    const none = { hasFindings: false, hasAdvisory: false, unanalyzedOnly: false, minReach: 0 };
    assert.equal(answersScope({ findings: 0 }, none), true, 'nothing asked, so everything answers');

    const asked = { ...none, hasFindings: true };
    assert.equal(answersScope({ findings: 2 }, asked), true);
    assert.equal(answersScope({ findings: 0 }, asked), false);

    assert.equal(answersScope({ advisories: 0 }, { ...none, hasAdvisory: true }), false);
    assert.equal(answersScope({ dependentRepos: 2 }, { ...none, minReach: 5 }), false);
    assert.equal(answersScope({ dependentRepos: 9 }, { ...none, minReach: 5 }), true);
});

test('a scope question answered by nothing on screen is still the reader asking it', () => {
    // Every question has to be able to come back empty-handed, or a filter that matches nothing
    // is indistinguishable from one that was never applied.
    const asked = { hasFindings: true, hasAdvisory: true, unanalyzedOnly: false, minReach: 0 };
    assert.equal(answersScope({ findings: 3, advisories: 0 }, asked), false, 'both questions apply');
    assert.equal(answersScope({ findings: 3, advisories: 1 }, asked), true);
});

// A context edge has to be followable, so it cannot use the emphasis fade (alpha 0.06, which is
// barely on the screen). It needs its own weight, and the palette gives colors in three notations.
test('an edge can be restated at a chosen weight, whatever notation its color arrived in', () => {
    assert.equal(withAlpha('#5566ff', 0.3), 'rgba(85,102,255,0.3)');
    assert.equal(withAlpha('#56f', 0.3), 'rgba(85,102,255,0.3)');
    assert.equal(withAlpha('rgb(85, 102, 255)', 0.3), 'rgba(85,102,255,0.3)');
    assert.equal(withAlpha('rgba(85, 102, 255, 0.9)', 0.3), 'rgba(85,102,255,0.3)');
    // Anything it cannot read is returned untouched rather than turned into a color nobody chose.
    assert.equal(withAlpha('currentColor', 0.3), 'currentColor');
});

// force-graph drops a click it decides followed a drag, by a heuristic we cannot see, tune or
// test — and a couple of pixels of trackpad drift between press and release is enough to lose the
// gesture entirely. Deciding it here is what makes it a rule rather than a surprise.
test('a gesture that barely moved is a click, and one that travelled is a drag', () => {
    assert.equal(isClick({ x: 100, y: 100 }, { x: 100, y: 100 }, 5), true);
    assert.equal(isClick({ x: 100, y: 100 }, { x: 103, y: 101 }, 5), true, 'trackpad drift lost the click');
    assert.equal(isClick({ x: 100, y: 100 }, { x: 130, y: 100 }, 5), false);
    // Exactly at the slop is still a drag, so the threshold has one meaning rather than two.
    assert.equal(isClick({ x: 100, y: 100 }, { x: 105, y: 100 }, 5), false);
});

test('a click with no press behind it is not a click', () => {
    // A pointerdown that began outside the canvas, or a synthetic click: nothing to measure
    // against, and acting on it would let a gesture the view never saw select a node.
    assert.equal(isClick(null, { x: 100, y: 100 }, 5), false);
    assert.equal(isClick({ x: 100, y: 100 }, null, 5), false);
});

test('a click selects, and a second on the same node isolates it', () => {
    const none = { key: '', at: 0 };

    assert.equal(clickAction('octo/app', none, 1000), 'select');
    assert.equal(clickAction('octo/app', { key: 'octo/app', at: 900 }, 1000), 'isolate');
    // A different node is a new question, however fast the reader moved.
    assert.equal(clickAction('octo/lib', { key: 'octo/app', at: 900 }, 1000), 'select');
    // Two deliberate clicks a second apart are two selections, not an isolate.
    assert.equal(clickAction('octo/app', { key: 'octo/app', at: 100 }, 1000), 'select');
});

test('a click with nothing near it is the way back out', () => {
    assert.equal(clickAction('', { key: 'octo/app', at: 900 }, 1000), 'clear');
});

// A dependency the queue fetched at several references is split into `name@ref` nodes and loses
// its bare key, while one fetched at a single reference keeps it. Both shapes are in the corpus,
// so a page handing over an action name lands on a node for some dependencies and on nothing for
// exactly the widely-referenced ones a remediation page is most likely to be about.
test('a focus naming an action finds it however the graph split it by reference', () => {
    const graph = {
        all: {
            nodes: [
                { key: 'octo/app' }, { key: 'actions/checkout@v3' }, { key: 'actions/checkout@v4' },
                { key: 'actions/setup-node' }, { key: 'other/checkout@v1' },
            ],
        },
    };

    assert.deepEqual(focusKeys(graph, 'actions/checkout'),
        ['actions/checkout@v3', 'actions/checkout@v4']);
    // An exact key is itself, and is never widened into its siblings.
    assert.deepEqual(focusKeys(graph, 'actions/setup-node'), ['actions/setup-node']);
    assert.deepEqual(focusKeys(graph, 'actions/checkout@v3'), ['actions/checkout@v3']);
    // A prefix that is not a whole name must not match: `other/checkout@v1` is a different action.
    assert.deepEqual(focusKeys(graph, 'checkout'), []);
    assert.deepEqual(focusKeys(graph, 'nothing/here'), []);
});

test('a neighborhood can start from every reference of one action', () => {
    const chain = {
        all: {
            nodes: [{ key: 'app' }, { key: 'dep@v1' }, { key: 'dep@v2' }, { key: 'leaf' }],
            links: [
                { source: 'app', target: 'dep@v1' },
                { source: 'dep@v2', target: 'leaf' },
            ],
        },
        adjacencies: null,
    };

    const seen = neighborhood(chain, ['dep@v1', 'dep@v2'], 1, 'both');

    // Both halves of the split are the subject, so both of their edges belong to the answer.
    assert.deepEqual([...seen.keys()].sort(), ['app', 'dep@v1', 'dep@v2', 'leaf']);
    assert.equal(seen.get('dep@v1'), 0, 'a subject is at zero hops, not one');
});

// Naming a node suspends the arrival flags, because otherwise `has findings` deletes the
// neighbourhood the reader just asked for. A focus that resolved to nothing has narrowed nothing,
// so suspending leaves the whole graph drawn with its default filter switched off — a saturated
// hairball arrived at by asking for one node.
test('a focus that named nothing does not suspend the filters it never narrowed', () => {
    const asked = { hasFindings: true, hasAdvisory: true, unanalyzedOnly: false, minReach: 4 };

    const resolved = scopeFilters({ ...asked, selection: 'octo/app' }, true);
    assert.equal(resolved.hasFindings, false);
    assert.equal(resolved.minReach, 0);

    const missing = scopeFilters({ ...asked, selection: 'octo/gone' }, false);
    assert.equal(missing.hasFindings, true, 'the default filter was switched off for no focus');
    assert.equal(missing.hasAdvisory, true);
    assert.equal(missing.minReach, 4);
});

// Coverage color is a reference ratio for collapsed groups, not their absent queue state.
test('coverage is colored by how much of a node has been analyzed, not by a queue state', () => {
    const palette = { muted: '#555', discovery: { queued: '#fc0', done: '#0c8' } };

    // A group of five dependencies, one of them analyzed: neither analyzed nor unanalyzed.
    const partial = coverageColor({ refsInUse: 5, refsAnalyzed: 1 }, palette);
    assert.notEqual(partial, palette.muted, 'a partly covered group was drawn as never queued');
    assert.notEqual(partial, palette.discovery.done);
    assert.notEqual(partial, palette.discovery.queued);

    assert.equal(coverageColor({ refsInUse: 5, refsAnalyzed: 5 }, palette), palette.discovery.done);
    assert.equal(coverageColor({ refsInUse: 5, refsAnalyzed: 0 }, palette), palette.discovery.queued);
});

// The legend names every queue state. A node none of whose references was analyzed carries the
// state that says why, and painting it queued would claim it is still waiting.
test('a node with nothing analyzed shows its own state under the coverage view', () => {
    const palette = { muted: '#555',
        discovery: { queued: '#fc0', done: '#0c8', unavailable: '#f00', nothing: '#80f' } };

    assert.equal(coverageColor({ refsInUse: 1, refsAnalyzed: 0, discovery: 'nothing' }, palette), '#80f');
    assert.equal(coverageColor({ refsInUse: 2, refsAnalyzed: 0, discovery: 'unavailable' }, palette), '#f00');
    // A fold carries no single state, so one with nothing analyzed is still waiting.
    assert.equal(coverageColor({ refsInUse: 5, refsAnalyzed: 0 }, palette), '#fc0');
});

test('a node that was never a dependency keeps saying so under the coverage view', () => {
    const palette = { muted: '#555', discovery: { queued: '#fc0', done: '#0c8' } };

    // No references in use is not zero coverage — it is a workflow inside the corpus under review,
    // which was never queued and must not be drawn in the same family as "we have not looked yet".
    assert.equal(coverageColor({ refsInUse: 0, refsAnalyzed: 0 }, palette), palette.muted);
    assert.equal(coverageColor({}, palette), palette.muted);
    // A state the node does carry still wins when there are no references to count.
    assert.equal(coverageColor({ discovery: 'done' }, palette), palette.discovery.done);
});

test('a region name that would collide is moved rather than dropped', () => {
    const hull = inflate(hullOf([
        { sx: 0, sy: 0 }, { sx: 400, sy: 0 }, { sx: 400, sy: 300 }, { sx: 0, sy: 300 },
    ]), 10);
    const canvas = { width: 800, height: 600 };
    const first = regionLabelSpot(hull, 120, 16, [], canvas);
    const taken = [{ x: first.x - 60, y: first.y - 8, w: 120, h: 16 }];
    const second = regionLabelSpot(hull, 120, 16, taken, canvas);

    assert.ok(second, 'the second name was dropped instead of moved');
    const rect = { x: second.x - 60, y: second.y - 8, w: 120, h: 16 };
    const t = taken[0];
    assert.ok(!(rect.x < t.x + t.w && t.x < rect.x + rect.w
        && rect.y < t.y + t.h && t.y < rect.y + rect.h), JSON.stringify([rect, t]));
});

// Boundary names must stay on-canvas even when the region sits against an edge.
test('a boundary name is kept on the canvas, however close to the edge its region sits', () => {
    const canvas = { width: 669, height: 533 };
    const corner = (x, y) => inflate(hullOf([
        { sx: x, sy: y }, { sx: x + 40, sy: y }, { sx: x + 40, sy: y + 30 }, { sx: x, sy: y + 30 },
    ]), 8);

    for (const [name, hull] of [
        ['right edge', corner(620, 120)],
        ['left edge', corner(2, 280)],
        ['top edge', corner(300, 4)],
        ['bottom edge', corner(300, 500)],
    ]) {
        const spot = regionLabelSpot(hull, 120, 16, [], canvas);
        assert.ok(spot, `${name}: no name placed at all`);
        assert.ok(spot.x - 60 >= 0 && spot.x + 60 <= canvas.width,
            `${name}: name runs off horizontally at x=${spot.x}`);
        assert.ok(spot.y - 8 >= 0 && spot.y + 8 <= canvas.height,
            `${name}: name runs off vertically at y=${spot.y}`);
    }
});

test('a name moved onto the canvas still points at the boundary it names', () => {
    // Clamping moves the name away from its region, so the line back to it stops being a nicety
    // and becomes the only thing identifying which boundary was named.
    const hull = inflate(hullOf([
        { sx: 630, sy: 10 }, { sx: 660, sy: 10 }, { sx: 660, sy: 40 }, { sx: 630, sy: 40 },
    ]), 8);

    const spot = regionLabelSpot(hull, 140, 16, [], { width: 669, height: 533 });

    assert.ok(spot.x + 70 <= 669, `the name still runs off at x=${spot.x}`);
    assert.ok(spot.leader, 'a name that had to move carries no way back to its region');
    assert.ok(spot.leader.x >= 600,
        `the line follows the name instead of pointing at the region: ${JSON.stringify(spot)}`);
});

// Clamping to the canvas collapses the ladder of candidates: every rung above a region at the top
// edge lands on the same clamped row, so one blocked position blocks all six. The real render left
// a boundary drawn with no name at all, which is the one thing this view may not do quietly.
test('a name with nowhere above it to go is placed elsewhere, not abandoned', () => {
    const canvas = { width: 669, height: 533 };
    const hull = inflate(hullOf([
        { sx: 60, sy: 4 }, { sx: 100, sy: 4 }, { sx: 100, sy: 30 }, { sx: 60, sy: 30 },
    ]), 8);
    // The caption owns the top-left strip, which is where clamping sends everything.
    const taken = [{ x: 0, y: 0, w: 300, h: 26 }];

    const spot = regionLabelSpot(hull, 120, 16, taken, canvas);

    assert.ok(spot, 'a boundary was drawn with no name at all');
    assert.ok(spot.x - 60 >= 0 && spot.x + 60 <= canvas.width, `off canvas at x=${spot.x}`);
    assert.ok(spot.y - 8 >= 0 && spot.y + 8 <= canvas.height, `off canvas at y=${spot.y}`);
    const rect = { x: spot.x - 60, y: spot.y - 8, w: 120, h: 16 };
    const t = taken[0];
    assert.ok(!(rect.x < t.x + t.w && t.x < rect.x + rect.w
        && rect.y < t.y + t.h && t.y < rect.y + rect.h), 'the name landed on the caption');
});

// Region helpers guard boundaries that look valid but assert false containment.

/** A repository laid out at a screen position, which is all a region decision needs. */
function placed(repository, sx, sy, extra = {}) {
    return { key: `${repository}/wf.yml`, kind: 'workflow', repository, sx, sy, ...extra };
}

test('no boundary is drawn around a node that belongs to somebody else', () => {
    // The C: two arms of one account with a stranger sitting in its mouth. A convex hull over the
    // whole group swallows the stranger, which states a containment that does not exist.
    const nodes = [
        placed('octo/a', 0, 0), placed('octo/b', 0, 120),
        placed('octo/c', 500, 0), placed('octo/d', 500, 120),
        placed('other/x', 250, 60),
    ];
    // Asserted so the fixture cannot quietly stop reproducing the case it exists for.
    const whole = inflate(hullOf(nodes.filter(n => n.repository.startsWith('octo/'))), 26);
    assert.ok(containsPoint(whole, { x: 250, y: 60 }), 'the fixture no longer forms a C');

    const { shapes, refused } = regionShapes(nodes, 'owner');

    for (const s of shapes) {
        for (const n of nodes) {
            if (s.nodes.includes(n)) continue;
            assert.equal(containsPoint(s.hull, { x: n.sx, y: n.sy }), false,
                `${s.key} encloses ${n.key}, which is not a member`);
        }
    }
    assert.equal(refused, 1, 'the group that could not be drawn honestly was not counted as refused');
});

// A computed layout already decided where a group sits, and its box is the shape it reserved for
// one. Re-deriving a hull from the members would draw a boundary the engine did not place, so the
// picture and the layout that produced it would disagree about where a group ends.
test('a boundary comes from the engine box when the layout supplied one', () => {
    const nodes = [placed('octo/a', 0, 0), placed('octo/b', 100, 100)];
    const boxes = new Map([['octo', { x1: -200, y1: -200, x2: 300, y2: 300 }]]);

    const { shapes } = regionShapes(nodes, 'owner', new Map(), undefined, boxes);

    assert.equal(shapes.length, 1);
    const xs = shapes[0].hull.map(p => p.x).sort((a, b) => a - b);
    const ys = shapes[0].hull.map(p => p.y).sort((a, b) => a - b);
    assert.deepEqual([xs[0], xs[3]], [-200, 300]);
    assert.deepEqual([ys[0], ys[3]], [-200, 300]);
});

// The containment invariant belongs to the boundary, not to the way it was computed. An engine
// box that swallows a stranger asserts a membership that does not exist just as loudly as a hull
// does, and `dot` is only known to separate its clusters on the graph it was given -- a browser
// filter drawing a subset of that graph is a different picture.
test('an engine box that encloses a stranger is refused like any other boundary', () => {
    const nodes = [placed('octo/a', 0, 0), placed('octo/b', 100, 100), placed('other/x', 50, 50)];
    const boxes = new Map([['octo', { x1: -200, y1: -200, x2: 300, y2: 300 }]]);

    const { shapes, groups, refused } = regionShapes(nodes, 'owner', new Map(), undefined, boxes);

    assert.equal(groups, 2);
    assert.equal(refused, 1, 'the engine box swallowing other/x was drawn anyway');
    assert.equal(shapes.some(s => s.key === 'octo'), false);
});

// A group the engine placed no box for still has members on screen. Falling through to the hull
// keeps the boundary rather than silently dropping it, which would read as a group that shares
// nothing.
test('a group the engine boxed nothing for keeps its hull', () => {
    const nodes = [
        placed('octo/a', 0, 0), placed('octo/b', 100, 0), placed('octo/c', 50, 100),
    ];
    const { shapes } = regionShapes(nodes, 'owner', new Map(), undefined, new Map());
    assert.equal(shapes.length, 1);
    assert.equal(shapes[0].key, 'octo');
});

// Nothing else on the canvas distinguishes a picture that will be identical tomorrow from one
// that is reshuffled on every reload, and under a stable layout the boundaries are the engine's
// rather than hulls over whatever the simulation settled on.
test('the canvas names the engine when it did not draw the picture itself', () => {
    const grouped = regionCaption(3, 0, 'owner');

    assert.equal(viewCaption(grouped, { producedBy: 'graphviz', engine: 'dot' }, { pinned: 9 }),
        'grouped by owner · dot layout');
    assert.equal(viewCaption('', { producedBy: 'graphviz', engine: 'dot' }, { pinned: 9 }),
        'dot layout');
});

// The default is the simulation, and captioning every ordinary view with a note about it is
// clutter on the one line reserved for what is missing from the picture.
test('the simulation is the default and says nothing extra', () => {
    assert.equal(viewCaption('grouped by owner', null, null), 'grouped by owner');
    assert.equal(viewCaption('', null, null), '');
});

// A partial layout is a partial claim. A reader told "dot layout" while a quarter of the nodes
// were placed by the simulation is being told the whole picture is reproducible.
test('a partly placed layout says how much of it the simulation drew', () => {
    const caption = viewCaption('', { producedBy: 'graphviz', engine: 'dot' }, { pinned: 6, free: 2 });
    assert.match(caption, /2 of 8 placed by the simulation/);
});

// An engine that could not be run is not a stable layout, and a caption that said nothing would
// leave the reader believing the control they just turned on is in effect.
test('a layout that could not be computed says so rather than going quiet', () => {
    const caption = viewCaption('', { producedBy: 'none', reason: 'layout engine is not installed: dot' }, null);
    assert.match(caption, /not installed/);
});

// A force-directed picture stops carrying information long before it stops rendering: past a few
// hundred nodes it is a hairball, and drawing one anyway trades a reader's attention for nothing.
// The ceiling is a refusal rather than a warning, because a warning over a drawn hairball is a
// note nobody reads next to a picture they are already trying to interpret.
test('a node set past the ceiling is withheld rather than drawn', () => {
    assert.deepEqual(drawDecision(301, false, 300), { draw: false, withheld: 301 });
    assert.deepEqual(drawDecision(3703, false, 300), { draw: false, withheld: 3703 });
});

// The boundary is inclusive: a reader who narrowed to exactly the limit asked for something the
// view promised to draw, and refusing it would make the number a lie.
test('the ceiling itself still draws', () => {
    assert.deepEqual(drawDecision(300, false, 300), { draw: true, withheld: 0 });
    assert.deepEqual(drawDecision(0, false, 300), { draw: true, withheld: 0 });
});

// Refusing something the reader has already looked at and accepted would make the control a loop
// rather than a gate.
test('an accepted graph draws at any size', () => {
    assert.deepEqual(drawDecision(12725, true, 300), { draw: true, withheld: 0 });
});

// A refusal a reader cannot see is worse than the boundary it withheld: the grouping control says
// it is grouping and the canvas shows nothing, with nothing saying which of the two is wrong. And
// `github` alone does not say whether it is an account or a business unit, so the level is stated
// once on the same line rather than repeated on every boundary.
test('one line says how the view is grouped and how much of it is missing', () => {
    const nodes = [
        placed('octo/a', 0, 0), placed('octo/b', 0, 120),
        placed('octo/c', 500, 0), placed('octo/d', 500, 120),
        placed('other/x', 250, 60), placed('other/y', 250, 90),
    ];
    const { groups, refused } = regionShapes(nodes, 'owner');

    assert.equal(groups, 2);
    assert.equal(regionCaption(groups, refused, 'owner'),
        'grouped by owner · 1 of 2 interleave with other groups and are not drawn');
    // Nothing withheld: the level still has to be stated, because nothing else on the canvas does.
    assert.equal(regionCaption(2, 0, 'owner'), 'grouped by owner');
    assert.equal(regionCaption(2, 0, 'repo'), 'grouped by repository');
    assert.equal(regionCaption(2, 0, 'grouping'), 'grouped by grouping');
});

// A boundary drawn without a name must be counted.
test('a boundary the canvas had no room to name says so', () => {
    assert.equal(regionCaption(23, 0, 'owner', 1),
        'grouped by owner · 1 has no room for its name');
    assert.equal(regionCaption(23, 0, 'owner', 3),
        'grouped by owner · 3 have no room for their names');
    // Both kinds of withholding at once, because they are different facts about the same picture:
    // one boundary could not be drawn honestly, another could not be named.
    assert.equal(regionCaption(24, 2, 'owner', 1),
        'grouped by owner · 2 of 24 interleave with other groups and are not drawn'
        + ' · 1 has no room for its name');
    assert.equal(regionCaption(23, 0, 'owner', 0), 'grouped by owner');
});

// If a boundary is refused, draw its container node again.
test('a container whose boundary was refused is drawn as a node again', () => {
    const account = { kind: 'org', owner: 'actions', key: 'org:actions' };

    assert.equal(drawnAsRegion(account, 'owner', false, new Set(['actions'])), true);
    assert.equal(drawnAsRegion(account, 'owner', false, new Set(['other'])), false,
        'the account is neither a point nor a boundary');
    // Before the first frame nothing has been measured, and flashing every container in and out
    // while the layout settles is worse than waiting one frame.
    assert.equal(drawnAsRegion(account, 'owner', false, null), true);
    assert.equal(drawnAsRegion(account, 'owner', true, null), false, 'a collapsed view wants the point');
});

// Boundary refusal is a fixed point because refused containers become obstacles.
test('the boundaries a view draws are a fixed point, reached before anything is painted', () => {
    const nodes = [
        // A C-shaped account with a stranger in its mouth: refused, so its own point comes back.
        placed('alpha/a', 0, 0), placed('alpha/b', 0, 120),
        placed('alpha/c', 400, 0), placed('alpha/d', 400, 120),
        placed('other/x', 200, 60),
        // The account node lands inside the next group's hull, which is only a problem once the
        // refusal above has put it on screen.
        { key: 'org:alpha', kind: 'org', owner: 'alpha', sx: 800, sy: 60 },
        placed('beta/a', 700, 0), placed('beta/b', 900, 0), placed('beta/c', 800, 140),
    ];

    const once = regionShapes(nodes.filter(n => n.kind !== 'org'), 'owner');
    assert.ok(once.shapes.some(s => s.key === 'beta'),
        'the fixture no longer needs a second pass to be wrong');

    const settled = settledRegions(nodes, 'owner');
    assert.ok(!settled.shapes.some(s => s.key === 'beta'),
        'beta encloses the account point that the first pass put back');
    assert.ok(!settled.drawn.has('alpha'));
    for (const s of settled.shapes) {
        for (const n of nodes) {
            if (s.nodes.includes(n) || settled.drawn.has(groupOf(n, 'owner'))) continue;
            assert.equal(containsPoint(s.hull, { x: n.sx, y: n.sy }), false,
                `${s.key} encloses ${n.key}`);
        }
    }
});

test('a region admits how many of its members are not on screen', () => {
    const drawn = [placed('octo/a', 0, 0), placed('octo/b', 40, 40), placed('octo/c', 0, 80)];
    // The loaded graph holds more of this account than the view is drawing, which is the ordinary
    // case: a filter runs underneath every scope.
    const loaded = [...drawn, { key: 'octo/d/wf.yml', kind: 'workflow', repository: 'octo/d' }];
    const { shapes } = regionShapes(drawn, 'owner', groupSizes(loaded, 'owner'));

    assert.equal(shapes.length, 1);
    assert.equal(shapes[0].shown, 3);
    assert.equal(shapes[0].total, 4);
    assert.equal(regionLabelParts('octo', shapes[0].shown, shapes[0].total, true).count, ' \u00b7 3 of 4');
});

// A region and the node a collapsed view folds the same members into are two renderings of one
// fold. Two rules is how they would come to disagree about who belongs together.
test('the regions drawn expanded are the nodes drawn collapsed', () => {
    const nodes = [
        placed('octo/a', 0, 0), placed('octo/b', 40, 40),
        placed('other/x', 300, 0), placed('other/y', 340, 40),
        placed('third/z', 600, 0),
    ];
    const links = [];

    const regions = new Set(regionShapes(nodes, 'owner').shapes.map(s => s.key));
    const folded = new Set(collapseGroups({ groupBy: 'owner' }, nodes, links).nodes
        .filter(n => n.kind === 'org')
        .map(n => n.owner));

    assert.deepEqual([...regions].sort(), [...folded].sort());
});

/** The share of the canvas two boundaries claim at once. */
function overlapRatio(shapes) {
    let shared = 0, total = 0;
    for (let i = 0; i < shapes.length; i++) {
        total += shapes[i].area;
        for (let j = i + 1; j < shapes.length; j++) {
            shared += overlapArea(shapes[i].hull, shapes[j].hull);
        }
    }
    return total ? shared / total : 0;
}

test('two boundaries do not claim the same nodes', () => {
    // Groups the layout has separated, which is what the clustering force exists to produce.
    const nodes = [
        placed('octo/a', 0, 0), placed('octo/b', 40, 40),
        placed('other/x', 400, 0), placed('other/y', 440, 40),
    ];

    assert.equal(overlapRatio(regionShapes(nodes, 'owner').shapes), 0);
});

// What a boundary is supposed to have replaced: the container point, and the edges whose whole
// content was the containment the boundary now states. Anything else it removes is a node or a
// relationship the reader has lost with nothing standing in for it.
test('a boundary removes its container and the edges that only said containment', () => {
    const octo = { key: 'org:octo', kind: 'org', owner: 'octo' };
    const other = { key: 'org:other', kind: 'org', owner: 'other' };
    const a = { key: 'octo/a', kind: 'repo', repository: 'octo/a' };
    const b = { key: 'other/b', kind: 'repo', repository: 'other/b' };
    const nodes = [octo, other, a, b];
    const links = [
        { source: octo, target: a, kind: 'contains' },
        { source: other, target: b, kind: 'contains' },
        // A real dependency, which no boundary states and none may remove.
        { source: a, target: b, kind: 'step-uses-action' },
    ];
    // Only `octo` could be drawn honestly; `other` was refused and keeps its point.
    const drawn = new Set(['octo']);

    const hidden = nodes.filter(n => drawnAsRegion(n, 'owner', false, drawn));
    assert.deepEqual(hidden.map(n => n.key), ['org:octo']);

    const dropped = links.filter(l => containmentIntoRegion(l, 'owner', false, drawn));
    assert.equal(dropped.length, 1);
    assert.equal(dropped[0].target.key, 'octo/a');

    // Folding is the other rendering of the same grouping and wants the points back.
    assert.equal(nodes.filter(n => drawnAsRegion(n, 'owner', true, drawn)).length, 0);
    assert.equal(links.filter(l => containmentIntoRegion(l, 'owner', true, drawn)).length, 0);
});

// A grouping has no container node, so nothing is removed at that level and a step that does
// nothing here has to be distinguishable from one that is broken.
test('grouping by a repository property hides no node and invents no bucket', () => {
    const nodes = [
        placed('octo/a', 0, 0, { grouping: 'payments' }),
        placed('octo/b', 40, 40, { grouping: 'payments' }),
        // No grouping at all: a dependency's placement is read from the organization, never
        // guessed from an edge, so it belongs to no unit and no boundary may enclose it.
        { key: 'actions/checkout', kind: 'action', isExternal: true, sx: 400, sy: 400 },
    ];
    const { shapes } = regionShapes(nodes, 'grouping');

    assert.deepEqual(shapes.map(s => s.key), ['payments']);
    assert.equal(containsPoint(shapes[0].hull, { x: 400, y: 400 }), false,
        'a dependency was enclosed by a unit it carries no value for');
});

test('what the page is told about a node is named fields, not the node the renderer owns', () => {
    const node = {
        key: 'actions/checkout@v4', kind: 'action', owner: 'actions', isExternal: true,
        dependentRepos: 17, reachKnown: true, refsInUse: 4, refsAnalyzed: 1,
        // The renderer and the simulation both hang state off the same object.
        x: 1, y: 2, vx: 0.1, __threeObj: { self: null },
    };
    node.__threeObj.self = node.__threeObj;
    const brief = nodeBrief(node);

    assert.equal(brief.key, 'actions/checkout@v4');
    assert.equal(brief.refsAnalyzed, 1);
    assert.ok(!('__threeObj' in brief), 'the mesh travelled across the interop boundary');
    assert.doesNotThrow(() => JSON.stringify(brief));
});

test('a node with no measurement does not travel as one with zero', () => {
    const brief = nodeBrief({ key: 'actions/new', kind: 'action' });

    assert.equal(brief.reachKnown, false);
    assert.equal(brief.dependentRepos, 0);
});

test('a brief says whether the declaration makes the node yours', () => {
    const yours = nodeBrief({ key: 'octo/app/.github/workflows/ci.yml', kind: 'workflow', origin: 'seed', owned: true });
    const seeded = nodeBrief({ key: 'other/tool/.github/workflows/ci.yml', kind: 'workflow', origin: 'seed' });

    assert.equal(yours.owned, true);
    assert.equal(seeded.owned, false, 'a starting point nobody declared travelled as yours');
});

// The server applies origins, and the browser applies them again to what it holds. Owned is the
// declaration's answer rather than a provenance, so it is asked of its own field.
test('origins=owned keeps what is yours, and seed still keeps every starting point', () => {
    const yours = { key: 'octo/app', origin: 'seed', owned: true };
    const seeded = { key: 'other/tool', origin: 'seed' };
    const external = { key: 'actions/checkout', origin: '' };

    assert.equal(answersOrigins(yours, new Set(['owned'])), true);
    assert.equal(answersOrigins(seeded, new Set(['owned'])), false);
    assert.equal(answersOrigins(yours, new Set(['seed'])), true);
    assert.equal(answersOrigins(seeded, new Set(['seed'])), true);
    assert.equal(answersOrigins(external, new Set(['external'])), true);
    assert.equal(answersOrigins(external, new Set(['owned', 'seed'])), false);
    assert.equal(answersOrigins(seeded, new Set()), true, 'no origin asked narrowed something');
});
