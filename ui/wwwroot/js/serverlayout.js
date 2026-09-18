// Applying a layout somebody else computed, rather than simulating one here.
//
// The server places nodes with Graphviz and hands back points; this turns them into canvas
// coordinates and pins each node there. The renderer, the hover, the selection and the delta
// merge are untouched -- what arrives is where things go, not a picture.
//
// A node the layout has no position for keeps the force simulation. Pinning it at the origin
// would put it somewhere nobody placed it, which is the same wrong answer as drawing an
// unmeasured node at zero.

// The span a layout is scaled onto. Graphviz measures in points and a corpus can be thousands
// wide; the canvas wants something on the order of the force simulation's own spread, so the
// camera's fit behaves the same whichever layout produced the positions.
export const DEFAULT_EXTENT = 600;

// fitPositions turns engine points into canvas coordinates, keyed by node.
//
// Scaled uniformly rather than stretched to the viewport: the aspect Graphviz chose carries the
// ranking, and squashing one axis to fill a window would flatten the very distance the layout
// was computed to show.
export function fitPositions(layout, { extent = DEFAULT_EXTENT } = {}) {
    const placed = new Map();
    if (!layout || !Array.isArray(layout.positions) || layout.positions.length === 0) {
        return placed;
    }
    const width = layout.width || 0;
    const height = layout.height || 0;
    const span = Math.max(width, height) || 1;
    const scale = extent / span;

    for (const p of layout.positions) {
        if (!p || typeof p.key !== 'string') continue;
        if (!Number.isFinite(p.x) || !Number.isFinite(p.y)) continue;
        placed.set(p.key, {
            x: (p.x - width / 2) * scale,
            // Graphviz measures up from the bottom left; the canvas measures down from its centre.
            y: (height / 2 - p.y) * scale,
        });
    }
    return placed;
}

// fitClusters turns cluster boxes into the same coordinates, so a boundary lands on its members.
export function fitClusters(layout, { extent = DEFAULT_EXTENT } = {}) {
    if (!layout || !Array.isArray(layout.clusters)) return [];
    const width = layout.width || 0;
    const height = layout.height || 0;
    const span = Math.max(width, height) || 1;
    const scale = extent / span;

    return layout.clusters.map(c => ({
        name: c.name,
        x1: (c.x1 - width / 2) * scale,
        x2: (c.x2 - width / 2) * scale,
        // Flipping y swaps which edge is the top, so the box is rebuilt rather than mapped.
        y1: (height / 2 - c.y2) * scale,
        y2: (height / 2 - c.y1) * scale,
    }));
}

// applyPositions pins each node the layout placed and frees the rest.
//
// Returns what it did, because "how much of this picture is the stable layout" is a question the
// interface has to be able to answer out loud.
export function applyPositions(nodes, placed) {
    let pinned = 0;
    let free = 0;
    for (const node of nodes ?? []) {
        const at = placed.get(node.key);
        if (!at) {
            releasePosition(node);
            free++;
            continue;
        }
        node.x = at.x;
        node.y = at.y;
        node.fx = at.x;
        node.fy = at.y;
        // A computed layout is planar. Leaving z free would let the simulation pull nodes out of
        // the plane the ranking was computed in.
        node.z = 0;
        node.fz = 0;
        pinned++;
    }
    return { pinned, free };
}

// releasePosition hands a node back to the simulation, which is what a drag has to do: a pinned
// node that cannot be moved reads as a broken canvas rather than as a deliberate layout.
export function releasePosition(node) {
    if (!node) return;
    node.fx = undefined;
    node.fy = undefined;
    node.fz = undefined;
}

// releaseAll drops a server layout entirely, for switching back to the simulation.
export function releaseAll(nodes) {
    for (const node of nodes ?? []) releasePosition(node);
}

// layoutSummary is what the interface says about the picture on screen.
//
// A reader looking at a stable layout and a reader looking at a force simulation are looking at
// two different kinds of claim, and nothing else on the canvas distinguishes them.
export function layoutSummary(layout, { pinned = 0, free = 0 } = {}) {
    if (!layout || layout.producedBy !== 'graphviz') {
        return {
            stable: false,
            text: layout?.reason
                ? `Force layout — ${layout.reason}`
                : 'Force layout — positions change on every reload',
        };
    }
    const parts = [`${layout.engine} layout`];
    if (free > 0) parts.push(`${free} of ${pinned + free} placed by the simulation`);
    if (layout.groupingDrawn === false && layout.reason) parts.push(layout.reason);
    return { stable: true, text: parts.join(' — ') };
}
