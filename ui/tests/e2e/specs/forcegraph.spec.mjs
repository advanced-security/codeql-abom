import { test, expect, ready, corpusSize } from '../harness.mjs';

// The layout claims the Explorer rests on have never been checked by anything that could see
// them. The audit that tried ran in a backgrounded tab: `document.visibilityState` was `hidden`,
// it recorded **0 animation frames in 2 seconds**, and two captures 90 seconds apart were
// pixel-identical — so every check reported "blocked" rather than pass or fail.
//
// That was a harness problem, and this is the harness. What a viewer still owns is hairball,
// hub occlusion and whether position reads as urgency; what a measurement owns is whether the
// simulation runs at all, whether a reload reproduces the same picture, and whether switching
// force re-orients rather than reshuffles. Those three are here, because the repo's own rule
// about the force switch is *measure it, don't eyeball it*.

/** The live instance's node positions, keyed. The payload is not it: the renderer owns these. */
const POSITIONS = async () => {
    const { instances } = await import('/js/state.js');
    const it = instances.get('abom-graph');
    if (!it) return null;
    return Object.fromEntries(
        it.graph.graphData().nodes.map((n) => [n.key, [n.x, n.y, n.z ?? 0]]),
    );
};

/** What the label loop thinks the view is doing: `none`, `idle` or `still`. */
async function restStateOf(page) {
    return page.evaluate(async () => {
        const { instances } = await import('/js/state.js');
        const it = instances.get('abom-graph');
        return it ? it.resting : 'gone';
    });
}

async function settled(page) {
    await expect.poll(() => restStateOf(page), {
        timeout: 40_000,
        message: 'the view never came to rest',
    }).toBe('still');
}

/** Polar coordinates, which is the only form the force-switch claim can be stated in. */
function polar([x, y]) {
    return { angle: Math.atan2(y, x), radius: Math.hypot(x, y) };
}

function median(values) {
    if (!values.length) return 0;
    const sorted = [...values].sort((a, b) => a - b);
    return sorted[Math.floor(sorted.length / 2)];
}

test.describe('the force-directed graph', () => {
    test.slow();

    // The precondition every other check here was blocked on. A probe that times a paused
    // renderer counts the browser's idle loop and reads 60 FPS for everything, so the frame
    // count is only evidence beside proof that something moved.
    test('the simulation actually advances in this browser', async ({ page, request, baseURL }) => {
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.nodes, 'nothing to lay out');

        await page.goto('/explore');
        await ready(page);
        await expect(page.locator('.abom-graph-host')).toBeVisible({ timeout: 30_000 });
        await settled(page);

        expect(await page.evaluate(() => document.visibilityState),
            'a backgrounded page runs no animation frames, so nothing below would mean anything')
            .toBe('visible');

        const run = await page.evaluate(async () => {
            const { instances } = await import('/js/state.js');
            const it = instances.get('abom-graph');
            const sample = it.graph.graphData().nodes[0];
            const before = [sample.x, sample.y];
            it.graph.resumeAnimation();
            it.graph.d3ReheatSimulation();
            let frames = 0;
            const tick = () => {
                frames += 1;
                requestAnimationFrame(tick);
            };
            requestAnimationFrame(tick);
            await new Promise((done) => setTimeout(done, 2000));
            return { frames, moved: sample.x !== before[0] || sample.y !== before[1] };
        });

        expect(run.frames, 'animation frames in two seconds').toBeGreaterThan(30);
        expect(run.moved, 'a sampled node moved, so the frames were drawing something').toBe(true);
        console.log(`  measured: ${run.frames} animation frames in 2s, node moved`);
    });

    // Spatial memory is the whole reason the layout is worth having. `seedAt` is deterministic in
    // the node key for exactly this: a reload that reshuffles costs the reader everything they
    // learned about where things are.
    test('a reload of the same scope draws the same graph', async ({ page, request, baseURL }) => {
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.nodes, 'nothing to lay out');

        const read = async () => {
            await page.goto('/explore');
            await ready(page);
            await expect(page.locator('.abom-graph-host')).toBeVisible({ timeout: 30_000 });
            await settled(page);
            return page.evaluate(POSITIONS);
        };

        const first = await read();
        const second = await read();

        expect(Object.keys(second).sort(), 'the same nodes came back')
            .toEqual(Object.keys(first).sort());

        // Stated as a distance rather than as equality: the relaxation runs for a number of
        // ticks, not to a fixed point, so two runs converge rather than coincide. The number is
        // measured, and a reshuffle is orders of magnitude away from it.
        const extent = Math.max(
            ...Object.values(first).map(([x, y]) => Math.hypot(x, y)),
        );
        const drift = Object.keys(first).map((key) =>
            Math.hypot(first[key][0] - second[key][0], first[key][1] - second[key][1]));
        const relative = median(drift) / extent;
        expect(relative, `median drift ${median(drift).toFixed(1)} of extent ${extent.toFixed(1)}`)
            .toBeLessThan(0.05);
        console.log(`  measured: ${Object.keys(first).length} nodes, median drift ` +
            `${median(drift).toFixed(1)} of extent ${extent.toFixed(0)} (${(relative * 100).toFixed(1)}%)`);
    });

    // The core design claim of the semantic forces, in the words the repo states it in: a switch
    // is a re-orientation, not a reshuffle. Angle carries who a node sits with and radius carries
    // what the view is about, so the first is preserved and the second is meant to change.
    test('switching force preserves angle and changes radius', async ({ page, request, baseURL }) => {
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.nodes, 'nothing to lay out');

        await page.goto('/explore');
        await ready(page);
        await expect(page.locator('.abom-graph-host')).toBeVisible({ timeout: 30_000 });
        await settled(page);
        const before = await page.evaluate(POSITIONS);

        await page.locator('.abom-view-label', { hasText: 'Priority' }).click();
        await settled(page);
        const after = await page.evaluate(POSITIONS);

        const shared = Object.keys(before).filter((key) => key in after);
        // Not an assertion, because too few nodes is a gap in the data set rather than a defect
        // in the layout. The seeded set draws 12 in both views — growing it from 75 nodes to 104
        // moved that by one, because what is scarce is the *intersection*: Priority draws what
        // carries a finding or an advisory route, and comparing angles across a dozen nodes says
        // nothing either way. Reported as not measured, so nobody reads a pass as evidence.
        test.skip(
            shared.length <= 20,
            `only ${shared.length} nodes appear in both views; too few to compare orientation`,
        );
        const turned = [];
        const stretched = [];
        for (const key of shared) {
            const a = polar(before[key]);
            const b = polar(after[key]);
            let delta = Math.abs(a.angle - b.angle);
            if (delta > Math.PI) delta = 2 * Math.PI - delta;
            turned.push((delta * 180) / Math.PI);
            stretched.push(Math.abs(a.radius - b.radius));
        }

        // Bounded where a re-orientation and a reshuffle actually part company, not at the
        // tightest number seen: `plans/ui/graph-layout.md` records that the simulation starts
        // unseeded, so how far it has converged when `settled` returns differs by machine — 4.3
        // degrees on the retired corpus, under 15 on CI, above it locally. A reshuffle lands near
        // 90, and that is the claim worth holding until the seed makes a tighter one honest.
        expect(median(turned), `median angular shift ${median(turned).toFixed(1)} degrees`)
            .toBeLessThan(45);
        expect(median(stretched), 'radius is what the switch is meant to change')
            .toBeGreaterThan(0);
        console.log(`  measured: ${shared.length} nodes, median angular shift ` +
            `${median(turned).toFixed(1)} deg, median radial change ${median(stretched).toFixed(1)}`);
    });

    // Kind is drawn as geometry as well as hue, which is the only thing that makes `action` red
    // against `workflow` green readable to somebody who cannot tell those apart. The geometry
    // comes from the separately vendored THREE and the material from the renderer's own copy, so
    // it is a runtime arrangement that a unit test cannot see and a screenshot cannot prove.
    test('a node kind is drawn as a shape and not only as a colour', async ({
        page,
        request,
        baseURL,
    }) => {
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.nodes, 'nothing to draw');

        await page.goto('/explore');
        await ready(page);
        await expect(page.locator('.abom-graph-host')).toBeVisible({ timeout: 30_000 });
        await settled(page);

        const drawn = await page.evaluate(async () => {
            const { instances } = await import('/js/state.js');
            const it = instances.get('abom-graph');
            const build = it.graph.nodeThreeObject();
            const byKind = new Map();
            for (const n of it.graph.graphData().nodes) {
                if (byKind.has(n.kind)) continue;
                const mesh = typeof build === 'function' ? build(n) : null;
                byKind.set(n.kind, mesh?.geometry?.uuid ?? null);
            }
            return { shapes: it.shapes, byKind: [...byKind] };
        });

        expect(drawn.shapes, 'the vendored THREE is usable, so shapes are in play').toBe(true);
        const kinds = drawn.byKind.filter(([, uuid]) => uuid);
        expect(kinds.length, 'kinds drawn with their own geometry').toBeGreaterThan(1);
        expect(new Set(kinds.map(([, uuid]) => uuid)).size, 'distinct geometries across kinds')
            .toBe(kinds.length);
        console.log(`  measured: ${kinds.length} kinds, each with its own geometry`);
    });
});
