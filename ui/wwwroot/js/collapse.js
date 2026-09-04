// Folding each group into a single node, and the merged edges between groups.

import { groupOf, edgeClass, UNGROUPED } from './topology.js';

/** Builds one node per group, merging the edges between them. */
export function collapseGroups(it, nodes, links) {
    const by = it.groupBy && it.groupBy !== 'none' ? it.groupBy : 'owner';
    const groups = new Map();

    for (const n of nodes) {
        // One named bucket rather than a group per node: an ungrouped node standing alone would
        // make a collapsed view look identical to an expanded one.
        const name = groupOf(n, by) || UNGROUPED;
        let g = groups.get(name);
        if (!g) {
            g = {
                key: `group:${name}`, kind: 'org', path: name, owner: name,
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
        const s = groups.get(groupOf(nodeByKey(nodes, e.source), by) || UNGROUPED);
        const t = groups.get(groupOf(nodeByKey(nodes, e.target), by) || UNGROUPED);
        if (!s || !t || s === t) continue;
        const id = `${s.key}>${t.key}`;
        const existing = merged.get(id);
        if (existing) { existing.count += e.count || 1; continue; }
        merged.set(id, {
            source: s.key, target: t.key, kind: 'group', refs: [], count: e.count || 1,
            cls: edgeClass(s, t), shown: true,
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
