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

// frameFor works out the transform from engine points to canvas coordinates.
//
// `drawn` narrows the frame to the nodes that will actually be on screen. The layout endpoint
// answers from server terms only, so it routinely places more than the browser draws -- measured
// on the crawled organization, 411 placed against 205 drawn -- and fitting to the engine's canvas
// frames the view for nodes it is not drawing, leaving the drawn ones in a corner of it.
//
// One transform for positions and boxes alike: a boundary fitted separately from its members is
// drawn around the wrong nodes.
function frameFor(layout, extent, drawn) {
    const width = layout.width || 0;
    const height = layout.height || 0;
    let minX = 0, minY = 0, maxX = width, maxY = height;

    if (drawn && drawn.size > 0) {
        minX = Infinity; minY = Infinity; maxX = -Infinity; maxY = -Infinity;
        for (const p of layout.positions || []) {
            if (!p || !drawn.has(p.key)) continue;
            if (!Number.isFinite(p.x) || !Number.isFinite(p.y)) continue;
            if (p.x < minX) minX = p.x;
            if (p.x > maxX) maxX = p.x;
            if (p.y < minY) minY = p.y;
            if (p.y > maxY) maxY = p.y;
        }
        // None of the drawn keys was placed, so there is no narrower frame to take.
        if (!Number.isFinite(minX)) {
            minX = 0; minY = 0; maxX = width; maxY = height;
        }
    }

    // Uniform, so the aspect the engine chose carries the ranking rather than being stretched out
    // of it. A single node has no extent and keeps the whole canvas's scale.
    const span = Math.max(maxX - minX, maxY - minY) || 1;
    return {
        scale: extent / span,
        midX: (minX + maxX) / 2,
        midY: (minY + maxY) / 2,
    };
}

// fitPositions turns engine points into canvas coordinates, keyed by node.
//
// Scaled uniformly rather than stretched to the viewport: the aspect Graphviz chose carries the
// ranking, and squashing one axis to fill a window would flatten the very distance the layout
// was computed to show.
export function fitPositions(layout, { extent = DEFAULT_EXTENT, drawn = null } = {}) {
    const placed = new Map();
    if (!layout || !Array.isArray(layout.positions) || layout.positions.length === 0) {
        return placed;
    }
    const { scale, midX, midY } = frameFor(layout, extent, drawn);

    for (const p of layout.positions) {
        if (!p || typeof p.key !== 'string') continue;
        if (!Number.isFinite(p.x) || !Number.isFinite(p.y)) continue;
        placed.set(p.key, {
            x: (p.x - midX) * scale,
            // Graphviz measures up from the bottom left; the canvas measures down from its centre.
            y: (midY - p.y) * scale,
        });
    }
    return placed;
}

// fitClusters turns cluster boxes into the same coordinates, so a boundary lands on its members.
export function fitClusters(layout, { extent = DEFAULT_EXTENT, drawn = null } = {}) {
    if (!layout || !Array.isArray(layout.clusters)) return [];
    const { scale, midX, midY } = frameFor(layout, extent, drawn);

    return layout.clusters.map(c => ({
        name: c.name,
        x1: (c.x1 - midX) * scale,
        x2: (c.x2 - midX) * scale,
        // Flipping y swaps which edge is the top, so the box is rebuilt rather than mapped.
        y1: (midY - c.y2) * scale,
        y2: (midY - c.y1) * scale,
    }));
}

// clusterBoxes keys the engine's boxes by the group each one encloses.
//
// The encoder names a subgraph `cluster_<by>_<key>`, so the prefix is stripped by length rather
// than split on: a repository or workflow key carries underscores of its own, and splitting would
// hand `octo/my_repo`'s box to `octo/my`. A box whose name does not carry this grouping's prefix
// belongs to some other level and is dropped -- drawn anyway it would be a boundary around
// members nobody grouped together.
export function clusterBoxes(layout, by, { extent = DEFAULT_EXTENT, drawn = null } = {}) {
    const out = new Map();
    if (!by || by === 'none') return out;
    const prefix = `cluster_${by}_`;
    for (const box of fitClusters(layout, { extent, drawn })) {
        if (typeof box.name !== 'string' || !box.name.startsWith(prefix)) continue;
        out.set(box.name.slice(prefix.length), box);
    }
    return out;
}

// refusalText turns a refused layout request into the sentence a reader gets.
//
// Size is the usual cause and the only one they can act on: `dot` is given ten seconds, and a
// scope of a few thousand nodes does not finish in them. Naming the count says which graph
// defeated it, rather than leaving "failed" over a picture that is still being drawn.
export function refusalText(body, status, nodes) {
    let message = '';
    try {
        message = (JSON.parse(body) || {}).error || '';
    } catch {
        // Not JSON at all, which is a proxy or a crash rather than the server's own answer.
    }
    if (/deadline|timeout|abandoned/i.test(message)) {
        return nodes
            ? `too large to place — ${nodes.toLocaleString('en-US')} nodes; narrow the scope`
            : 'too large to place — narrow the scope';
    }
    return message || `layout refused (${status})`;
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
