import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
    seed, repoOf, orgOf, edgeClass, groupOf, anchorKey, keyOf, faded,
    nodeRadius, isSelected, particleSpeed, needsRender, collapseGroups, ungroupedName,
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
    const collapsed = { seeded: true, subset: false, collapsed: true, groupBy: 'owner' };
    assert.equal(needsRender(collapsed, false), true);

    // The fold depends on the filtered edges too, so an unchanged grouping is not enough to
    // stay on the cheap path.
    assert.equal(needsRender({ ...collapsed, foldedBy: 'owner' }, false), true);
});

test('an expanded graph is not rebuilt just because the grouping changed', () => {
    // Grouping only moves an expanded graph, so it must stay on the cheap path.
    const it = { seeded: true, subset: false, collapsed: false, groupBy: 'owner', foldedBy: null };
    assert.equal(needsRender(it, false), false);
    assert.equal(needsRender({ ...it, seeded: false }, false), true, 'a first draw always renders');
    assert.equal(needsRender(it, true), true, 'entering a subset changes the node set');
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

test('the ungrouped bucket is named after the grouping that left it out', () => {
    // It read "(not in a workflow)" even when grouping by repository, which describes nothing.
    assert.equal(ungroupedName('repo'), '(not in a repository)');
    assert.equal(ungroupedName('owner'), '(no account)');
    assert.equal(ungroupedName('workflow'), '(not in a workflow)');
});
