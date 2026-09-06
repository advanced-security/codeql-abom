import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
    seed, repoOf, orgOf, edgeClass, groupOf, anchorKey, keyOf, faded,
    nodeRadius, isSelected, particleSpeed, needsRender, collapseGroups, ungroupedName,
    mergeInto, placeArrivals, neighborhood, discoveryColor, escapesGraph,
    simulationSet, visibleSignature, restLevel, REST_DELAY, REST_FPS, fullyAnalyzed, labelFor,
} from './graph.js';

// These are the pure decisions behind the whole view: which edges are drawn, how big a node is,
// what groups with what. Every bug this file has had lived in one of them, and none of it was
// reachable by any test until now.

test('an edge is classified by the closest true relationship', () => {
    const a = { key: 'octo/app/.github/workflows/ci.yml', repository: 'octo/app' };
    const b = { key: 'octo/app/.github/actions/build', repository: 'octo/app' };
    const c = { key: 'octo/other/.github/workflows/ci.yml', repository: 'octo/other' };
    const d = { key: 'actions/checkout' };

    assert.equal(edgeClass(a, b), 'sameRepo');
    // Same repository outranks same owner, or the broadest reading would always win.
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

test('grouping by workflow does not put every node in a group of its own', () => {
    // The bug: with steps folded away nothing has a container, so each node became its own
    // group and collapsing changed nothing at all.
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

    // Selecting used to multiply by 2.2, which filled the canvas and hid the neighborhood the
    // selection was meant to reveal.
    const selected = nodeRadius(hub, new Set([hub.key]));
    assert.ok(selected / nodeRadius(hub) < 1.5, `selected node grew ${selected / nodeRadius(hub)}x`);
});

test('an owner is sized as a container rather than by the reach of its largest member', () => {
    // Inheriting reach drew the account larger than the dependency inside it, counting one
    // member's reach twice on screen.
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
    // force-graph replaces the string with the node object once seeded; both forms occur.
    assert.equal(keyOf('actions/checkout'), 'actions/checkout');
    assert.equal(keyOf({ key: 'actions/checkout' }), 'actions/checkout');
});

test('fading an edge changes its alpha and keeps its hue', () => {
    assert.equal(faded('rgba(255,110,156,0.85)'), 'rgba(255,110,156,0.06)');
    assert.equal(faded('rgb(255,110,156)'), 'rgba(255,110,156,0.06)');
    assert.equal(faded('#ff6e9c'), '#ff6e9c', 'a hex colour has no alpha to drop');
});

test('layout and animation are deterministic for the same corpus', () => {
    // Positions are seeded by key so the same graph always lays out identically; drift would
    // destroy the spatial memory a reader builds up.
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

    // A traverse under about four seconds at 60fps reads as a pulse, which is what the shared
    // speed used to produce.
    assert.ok(1 / (fastest * 60) > 4, `fastest traverse is ${(1 / (fastest * 60)).toFixed(1)}s`);
    // Distinct periods are what pull them out of lockstep; there is no phase offset available.
    assert.ok(fastest / slowest > 2, `speed spread is only ${(fastest / slowest).toFixed(1)}x`);
});

test('a collapsed graph is rebuilt whenever anything it is folded from changes', () => {
    // Two bugs, one cause: applyFilters skipped render() whenever the graph was already seeded,
    // but the fold is built inside render(). Regrouping relabeled the caption while leaving the
    // previous grouping on screen, and hiding an edge class left its merged edges drawn.
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

// The queue analyzes a reference; the graph draws a name. `analyzed` is true as soon as ONE
// reference has results, so the "not analyzed" filter excluded 467 partially covered actions on
// the real corpus — including actions/checkout at 1 of 27. The filter whose entire job is finding
// unanalyzed work was hiding the largest gaps in it.
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
        'actions/checkout@v4 \u00b7 77 repos');
    assert.equal(labelFor({ key: 'actions/checkout@v4', dependentRepos: 0 }),
        'actions/checkout@v4', 'nothing depending on it is not a count worth printing');
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
