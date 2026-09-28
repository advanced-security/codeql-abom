// The simulation's forces. Position has exactly one meaning at a time, which is why the
// semantic force is assigned rather than added to.

import { anchorKey, groupOf, ungroupedName } from './topology.js';
import { isSelected } from './appearance.js';

/** Pulls each node towards a radius the active view decides. */
export function radialForce(radiusOf, strength) {
    let nodes = [];
    function force(alpha) {
        for (const n of nodes) {
            const target = radiusOf(n);
            if (target === null || target === undefined) continue;
            const x = n.x || 1e-6, y = n.y || 1e-6, z = n.z || 0;
            const dist = Math.sqrt(x * x + y * y + z * z) || 1e-6;
            const k = (target - dist) * strength * alpha / dist;
            n.vx += x * k;
            n.vy += y * k;
            if (n.vz !== undefined) n.vz += z * k;
        }
    }
    force.initialize = (n) => { nodes = n; };
    return force;
}

/**
 * Draws nodes back towards the origin, hardest on the ones with fewest links.
 *
 * Repulsion acts on every pair but only edges pull back; `spread` weakens x-axis gravity so the
 * settled cloud matches the canvas aspect.
 */
export function gravityForce(strengthOf, spread = 1) {
    let nodes = [];
    const wide = Math.max(spread, 0.01);
    function force(alpha) {
        for (const n of nodes) {
            const s = strengthOf(n) * alpha;
            n.vx -= (n.x || 0) * s / wide;
            n.vy -= (n.y || 0) * s;
            if (n.vz !== undefined) n.vz -= (n.z || 0) * s;
        }
    }
    force.initialize = (n) => { nodes = n; };
    return force;
}

/**
 * How much to weaken gravity across the wide axis to spread the cloud by `aspect`.
 *
 * Repulsion falls off with square distance, so use a cubic correction; cap it so gravity still
 * constrains low-degree nodes.
 */
export function spreadFor(aspect) {
    return Math.min(Math.max(aspect, 1) ** 3, 12);
}

export function applyForces(it) {
    const g = it.graph;
    const spread = it.spread || 600;
    const degree = it.degree || new Map();
    // The shape of the room the layout has to settle into. A view that is twice as wide as it is
    // tall should produce a cloud of about those proportions, not a circle with empty sides.
    const box = it.el ? it.el.getBoundingClientRect() : null;
    const aspect = box && box.height > 0 ? box.width / box.height : 1;
    // No distance cap: capping repulsion below the graph's own span means the outer nodes feel
    // nothing and the whole layout collapses inward into a speck.
    g.d3Force('charge').strength(-90).distanceMax(Infinity);
    g.d3Force('link').distance(36).strength(0.6);
    g.d3Force('gravity',
        gravityForce(n => 0.012 + 0.075 / (1 + (degree.get(n.key) || 0)), spreadFor(aspect)));
    g.d3Force('semantic', null);

    if (it.view === 'priority') {
        const max = it.maxPriority;
        g.d3Force('semantic', radialForce(
            n => isSelected(it.selected, n.key) ? 0 : spread * 0.15
                + (1 - (n.priority || 0) / max) * spread, 0.09));
    } else if (it.view === 'ownership') {
        // Distance, not left-versus-right: the camera can orbit in 3D.
        g.d3Force('semantic', radialForce(n => {
            const hops = it.hopsOf ? it.hopsOf.get(n.key) : undefined;
            if (hops === undefined) return null;
            if (hops === 0) return 0;
            return hops === 1 ? spread * 0.3 : spread;
        }, 0.09));
    } else if (it.view === 'distance') {
        g.d3Force('semantic', radialForce(n => {
            const hops = it.hopsOf ? it.hopsOf.get(n.key) : undefined;
            return hops === undefined ? null : spread * 0.2 + hops * spread * 0.42;
        }, 0.11));
    } else if (it.view === 'frontier') {
        // Rings are hops from the corpus under review; nodes with no dependency depth stay structural.
        g.d3Force('semantic', radialForce(n => {
            if (!n.discovery) return null;
            return spread * 0.2 + (n.depth || 1) * spread * 0.34;
        }, 0.1));
    }

    // Collapsed groups are already single nodes.
    g.d3Force('grouping', it.groupBy && it.groupBy !== 'none' && !it.collapsed
        ? clusterForce(it, spread)
        : null);
}

/**
 * Pulls members of a group toward the node that already represents it.
 *
 * Uses the owner, repository, or workflow node when one exists; property-only groupings fall back
 * to the running centroid of their members.
 */
function clusterForce(it, spread) {
    let nodes = [];
    let centerOf = new Map();
    let groupOf2 = new Map();
    const centroids = new Map();

    const force = alpha => {
        // Boundaries state containment, so keep grouped members close enough for honest hulls.
        const k = alpha * 0.26;

        if (groupOf2.size) {
            centroids.clear();
            for (const [n, key] of groupOf2) {
                if (n.x === undefined) continue;
                const c = centroids.get(key) || { x: 0, y: 0, z: 0, count: 0 };
                c.x += n.x; c.y += n.y; c.z += n.z || 0; c.count++;
                centroids.set(key, c);
            }
            for (const c of centroids.values()) {
                c.x /= c.count; c.y /= c.count; c.z /= c.count;
            }
        }

        for (const n of nodes) {
            const c = centerOf.get(n) || centroids.get(groupOf2.get(n));
            if (!c || c === n || c.x === undefined) continue;
            n.vx += (c.x - n.x) * k;
            n.vy += (c.y - n.y) * k;
            if (it.use3d) n.vz += (c.z - n.z) * k;
        }
    };
    force.initialize = ns => {
        nodes = ns;
        const byKey = new Map(ns.map(n => [n.key, n]));
        centerOf = new Map();
        groupOf2 = new Map();
        for (const n of ns) {
            const anchor = byKey.get(anchorKey(n, it.groupBy));
            if (anchor && anchor !== n) {
                centerOf.set(n, anchor);
                continue;
            }
            // No node stands for this group, so its members gather around themselves.
            const key = groupOf(n, it.groupBy);
            if (key && key !== ungroupedName(it.groupBy)) groupOf2.set(n, key);
        }
    };
    return force;
}
