// Folding each group into a single node, and the merged edges between groups.

import { groupOf, edgeClass, ungroupedName } from './topology.js';

/** Builds one node per group, merging the edges between them. */
export function collapseGroups(it, nodes, links) {
    const by = it.groupBy && it.groupBy !== 'none' ? it.groupBy : 'owner';
    const groups = new Map();

    for (const n of nodes) {
        // One named bucket rather than a group per node: an ungrouped node standing alone would
        // make a collapsed view look identical to an expanded one.
        const name = groupOf(n, by) || ungroupedName(by);
        let g = groups.get(name);
        if (!g) {
            g = {
                key: `group:${name}`, kind: 'org', path: name, ...identityOf(name, by),
                members: 0, findings: 0, dependentRepos: 0, rules: [], advisories: 0,
                analyzed: true, isExternal: true, refStyles: new Set(), edgeClasses: new Set(),
                // Seeded from the members' own center so a collapsed view sits where the
                // expanded one did.
                x: 0, y: 0, z: 0,
            };
            groups.set(name, g);
        }
        g.members++;
        g.findings += n.findings || 0;
        g.dependentRepos = Math.max(g.dependentRepos, n.dependentRepos || 0);
        if (!n.isExternal) g.isExternal = false;
        if (n.x !== undefined) { g.x += n.x; g.y += n.y; g.z += n.z || 0; }
        n.groupKey = g.key;
    }
    for (const g of groups.values()) {
        g.x /= g.members; g.y /= g.members; g.z /= g.members;
    }

    const merged = new Map();
    for (const e of links) {
        if (!e.shown) continue;
        const s = groups.get(groupOf(nodeByKey(nodes, e.source), by) || ungroupedName(by));
        const t = groups.get(groupOf(nodeByKey(nodes, e.target), by) || ungroupedName(by));
        if (!s || !t || s === t) continue;
        const id = `${s.key}>${t.key}`;
        const existing = merged.get(id);
        if (existing) { existing.count += e.count || 1; continue; }
        // A merged edge is a relationship in its own right and may not be the class any of its
        // members were: two same-owner edges can merge into one that crosses accounts. It has to
        // be judged as what it is, or a hidden class reappears once the graph is folded.
        const cls = edgeClass(s, t);
        merged.set(id, {
            source: s.key, target: t.key, kind: 'group', refs: [], count: e.count || 1,
            cls, shown: !it.edgeClasses || it.edgeClasses.has(cls),
        });
    }
    return { nodes: [...groups.values()], links: [...merged.values()] };
}

function nodeByKey(nodes, key) {
    if (!nodes.byKey) {
        nodes.byKey = new Map(nodes.map(n => [n.key, n]));
    }
    return nodes.byKey.get(key) || { key };
}

/**
 * What a group *is*, so that edges between groups classify by the real relationship.
 *
 * A group folded by repository still sits inside an account, and two of them under one account
 * are a same-owner edge rather than a cross-owner one. Without this a collapsed graph reported
 * every relationship as the same kind.
 */
function identityOf(name, by) {
    if (name === ungroupedName(by)) return { owner: '' };
    switch (by) {
        case 'repo':
            return { repository: name, owner: name.split('/')[0] };
        case 'workflow':
            return {
                repository: name.split('/').slice(0, 2).join('/'),
                owner: name.split('/')[0],
            };
        default:
            return { owner: name };
    }
}
