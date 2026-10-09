// Pure screen-space geometry for visible group regions.

import { groupOf, ungroupedName } from './topology.js';

/**
 * Members per region, keyed by group.
 *
 * Membership comes from `collapse.js`; the ungrouped bucket is not a region.
 */
export function regionMembers(nodes, by) {
    const out = new Map();
    if (!by || by === 'none') return out;
    const ungrouped = ungroupedName(by);

    for (const n of nodes) {
        if (n.sx === undefined || n.sy === undefined) continue;
        const key = groupOf(n, by);
        if (!key || key === ungrouped) continue;
        const members = out.get(key);
        if (members) members.push(n);
        else out.set(key, [n]);
    }
    return out;
}

/**
 * How many nodes each group holds, whether or not the layout has placed them.
 */
export function groupSizes(nodes, by) {
    const out = new Map();
    if (!by || by === 'none') return out;
    const ungrouped = ungroupedName(by);

    for (const n of nodes) {
        const key = groupOf(n, by);
        if (!key || key === ungrouped) continue;
        out.set(key, (out.get(key) || 0) + 1);
    }
    return out;
}

/**
 * How far a boundary sits outside its members.
 *
 * Scales with member extent; the floor keeps singletons drawable and the ceiling caps large groups.
 */
export function regionPadding(points, floor, ceiling) {
    let lo = Infinity, hi = -Infinity, top = Infinity, bottom = -Infinity;
    for (const p of points) {
        lo = Math.min(lo, p.sx); hi = Math.max(hi, p.sx);
        top = Math.min(top, p.sy); bottom = Math.max(bottom, p.sy);
    }
    const extent = Math.max(hi - lo, bottom - top);
    if (!(extent > 0)) return floor;
    return Math.min(ceiling, Math.max(floor, extent * PADDING_RATIO));
}

const PADDING_RATIO = 0.085;

/**
 * The convex hull of a set of placed nodes, by Andrew's monotone chain.
 *
 * Returned counter-clockwise. Fewer than three points is returned as given.
 */
export function hullOf(points) {
    const pts = points
        .map(p => ({ x: p.sx, y: p.sy }))
        .sort((a, b) => a.x - b.x || a.y - b.y);
    if (pts.length < 3) return pts;

    const half = source => {
        const chain = [];
        for (const p of source) {
            while (chain.length >= 2 && cross(chain[chain.length - 2], chain[chain.length - 1], p) <= 0) {
                chain.pop();
            }
            chain.push(p);
        }
        chain.pop();
        return chain;
    };
    return half(pts).concat(half([...pts].reverse()));
}

/**
 * A rectangle the layout engine reserved for a group, as a hull the rest of this module can use.
 *
 * Counter-clockwise to match `hullOf`; already padded by the layout engine.
 */
export function boxHull(box) {
    if (!box) return [];
    const { x1, y1, x2, y2 } = box;
    if (![x1, y1, x2, y2].every(Number.isFinite)) return [];
    return [{ x: x1, y: y1 }, { x: x2, y: y1 }, { x: x2, y: y2 }, { x: x1, y: y2 }];
}

/** The unsigned area used to compare boundaries and detect degenerate hulls. */
export function hullArea(hull) {
    if (!hull || hull.length < 3) return 0;
    return Math.abs(signedArea(hull));
}

/**
 * Pushes the boundary outward so it clears the node shapes rather than cutting through them.
 *
 * Also what gives a degenerate hull a body: one member is a point and collinear members are a line,
 * and neither can be stroked as a region until it has been given width.
 */
export function inflate(hull, padding) {
    if (!hull.length) return [];
    if (hull.length === 1) return circle(hull[0], padding);
    if (hull.length === 2 || hullArea(hull) === 0) return capsule(hull, padding);

    const cx = hull.reduce((s, p) => s + p.x, 0) / hull.length;
    const cy = hull.reduce((s, p) => s + p.y, 0) / hull.length;
    return hull.map(p => {
        const dx = p.x - cx, dy = p.y - cy;
        const len = Math.hypot(dx, dy) || 1;
        return { x: p.x + (dx / len) * padding, y: p.y + (dy / len) * padding };
    });
}

function circle(center, radius, segments = 16) {
    const out = [];
    for (let i = 0; i < segments; i++) {
        const a = (i / segments) * Math.PI * 2;
        out.push({ x: center.x + Math.cos(a) * radius, y: center.y + Math.sin(a) * radius });
    }
    return out;
}

// A rounded band around the line the members lie on, which is what a two-member or collinear group
// is. Built from the extremes, so it survives however many points sit between them.
function capsule(hull, padding) {
    const a = hull[0], b = hull[hull.length - 1];
    const dx = b.x - a.x, dy = b.y - a.y;
    const len = Math.hypot(dx, dy);
    if (!len) return circle(a, padding);
    const nx = (-dy / len) * padding, ny = (dx / len) * padding;
    const ex = (dx / len) * padding, ey = (dy / len) * padding;
    return [
        { x: a.x + nx - ex, y: a.y + ny - ey },
        { x: b.x + nx + ex, y: b.y + ny + ey },
        { x: b.x - nx + ex, y: b.y - ny + ey },
        { x: a.x - nx - ex, y: a.y - ny - ey },
    ];
}

/**
 * Whether a boundary encloses a point, by ray casting.
 *
 * Half-open on the vertical span so a ray leaving through a vertex is counted once, which is the
 * case a naive crossing count gets wrong — and it gets it wrong in the reader's favour, reporting
 * a node as outside a boundary that does enclose it.
 */
export function containsPoint(polygon, point) {
    let inside = false;
    for (let i = 0, j = polygon.length - 1; i < polygon.length; j = i++) {
        const a = polygon[i], b = polygon[j];
        if ((a.y > point.y) === (b.y > point.y)) continue;
        const x = a.x + ((point.y - a.y) / (b.y - a.y)) * (b.x - a.x);
        if (point.x < x) inside = !inside;
    }
    return inside;
}

/**
 * The area two boundaries share, by Sutherland–Hodgman clipping.
 *
 * Both arguments must be convex, which everything `inflate` returns is. Overlap is the measurement
 * that says whether grouping is doing anything: two boundaries covering the same pixels assert two
 * containments over one set of nodes.
 */
export function overlapArea(subject, clip) {
    if (!subject || !clip || subject.length < 3 || clip.length < 3) return 0;
    const ccw = signedArea(clip) > 0;
    let out = subject;

    for (let i = 0; i < clip.length && out.length; i++) {
        const a = clip[i], b = clip[(i + 1) % clip.length];
        const inside = p => (ccw ? cross(a, b, p) >= 0 : cross(a, b, p) <= 0);
        const next = [];
        for (let j = 0; j < out.length; j++) {
            const cur = out[j], prev = out[(j + out.length - 1) % out.length];
            if (inside(cur)) {
                if (!inside(prev)) next.push(intersect(prev, cur, a, b));
                next.push(cur);
            } else if (inside(prev)) {
                next.push(intersect(prev, cur, a, b));
            }
        }
        out = next;
    }
    return hullArea(out);
}

function signedArea(poly) {
    let sum = 0;
    for (let i = 0; i < poly.length; i++) {
        const a = poly[i], b = poly[(i + 1) % poly.length];
        sum += a.x * b.y - b.x * a.y;
    }
    return sum / 2;
}

const cross = (o, a, b) => (a.x - o.x) * (b.y - o.y) - (a.y - o.y) * (b.x - o.x);

function intersect(p, q, a, b) {
    const d = (q.x - p.x) * (b.y - a.y) - (q.y - p.y) * (b.x - a.x);
    if (!d) return { x: q.x, y: q.y };
    const t = ((a.x - p.x) * (b.y - a.y) - (a.y - p.y) * (b.x - a.x)) / d;
    return { x: p.x + (q.x - p.x) * t, y: p.y + (q.y - p.y) * t };
}
