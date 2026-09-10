// The simulation's forces. Position has exactly one meaning at a time, which is why the
// semantic force is assigned rather than added to.

import { anchorKey } from './topology.js';
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
 * Repulsion acts on every pair but only edges pull back, so barely-connected and orphaned nodes
 * accelerate outward with nothing to stop them. They end up owning most of the bounding box,
 * which leaves the dense part of the graph rendered as a speck and makes zoom feel dead.
 */
export function gravityForce(strengthOf) {
    let nodes = [];
    function force(alpha) {
        for (const n of nodes) {
            const s = strengthOf(n) * alpha;
            n.vx -= (n.x || 0) * s;
            n.vy -= (n.y || 0) * s;
            if (n.vz !== undefined) n.vz -= (n.z || 0) * s;
        }
    }
    force.initialize = (n) => { nodes = n; };
    return force;
}

export function applyForces(it) {
    const g = it.graph;
    const spread = it.spread || 600;
    const degree = it.degree || new Map();
    // No distance cap: capping repulsion below the graph's own span means the outer nodes feel
    // nothing and the whole layout collapses inward into a speck.
    g.d3Force('charge').strength(-90).distanceMax(Infinity);
    g.d3Force('link').distance(36).strength(0.6);
    g.d3Force('gravity', gravityForce(n => 0.012 + 0.075 / (1 + (degree.get(n.key) || 0))));
    g.d3Force('semantic', null);

    if (it.view === 'priority') {
        const max = it.maxPriority;
        g.d3Force('semantic', radialForce(
            n => isSelected(it.selected, n.key) ? 0 : spread * 0.15
                + (1 - (n.priority || 0) / max) * spread, 0.09));
    } else if (it.view === 'ownership') {
        // Distance, not left-versus-right: an axis stops meaning anything the moment the camera
        // orbits, and this has to read the same in 2D and 3D. Reached directly sits close in,
        // reached through an intermediary sits out.
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
        // Rings are hops from the corpus under review, so the picture reads outward: what is
        // known at the center, what is queued beyond it. Anything that was never a dependency
        // has no depth and is left where the structure puts it.
        g.d3Force('semantic', radialForce(n => {
            if (!n.discovery) return null;
            return spread * 0.2 + (n.depth || 1) * spread * 0.34;
        }, 0.1));
    }

    // Collapsed, each group is already a single node, so pulling groups together would only
    // gather unrelated nodes into a knot.
    g.d3Force('grouping', it.groupBy && it.groupBy !== 'none' && !it.collapsed
        ? clusterForce(it, spread)
        : null);
}

/**
 * Pulls members of a group toward the node that already represents it.
 *
 * The center is the owner, repository or workflow node itself, not a position invented for the
 * group. Synthetic centers placed around a ring fought the link forces: a workflow was dragged
 * toward its owner's arbitrary spot while its dependencies pulled it somewhere else, and the
 * result crossed the whole diagram. Anchoring to a real node means grouping and structure agree.
 */
function clusterForce(it, spread) {
    let nodes = [];
    let centerOf = new Map();

    const force = alpha => {
        const k = alpha * 0.09;
        for (const n of nodes) {
            const c = centerOf.get(n);
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
        for (const n of ns) {
            const anchor = byKey.get(anchorKey(n, it.groupBy));
            if (anchor && anchor !== n) centerOf.set(n, anchor);
        }
    };
    return force;
}
