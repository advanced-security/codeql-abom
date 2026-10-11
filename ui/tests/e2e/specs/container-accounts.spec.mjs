import { test, expect, ready, on } from '../harness.mjs';

test('images stay visible without an account boundary, expanded and collapsed',
    on('/explore'), async ({ page, request }) => {
        const response = await request.get('/api/graph/full?kinds=docker_image');
        expect(response.ok()).toBeTruthy();
        const graph = await response.json();
        const images = graph.nodes.filter(n => n.kind === 'docker_image').map(n => n.key).sort();
        test.skip(images.length === 0, 'the corpus has no container images');

        await page.addInitScript(() => {
            window.accountCaptions = [];
            const fillText = CanvasRenderingContext2D.prototype.fillText;
            CanvasRenderingContext2D.prototype.fillText = function (text, ...args) {
                if (text.includes('no account boundary')) window.accountCaptions.push(text);
                return fillText.call(this, text, ...args);
            };
        });
        await page.goto('/explore?kinds=docker_image&groupBy=owner');
        await ready(page);

        const drawn = () => page.evaluate(async () => {
            const { instances } = await import('/js/state.js');
            const it = instances.get('abom-graph');
            return it?.graph.graphData().nodes.map(n => n.key).sort() ?? [];
        });
        await expect.poll(drawn).toEqual(images);
        await expect.poll(() => page.evaluate(() => window.accountCaptions)).toContain(
            `grouped by owner · ${images.length} ${images.length === 1 ? 'node has' : 'nodes have'} no account boundary`);

        await page.getByRole('checkbox', { name: 'Collapse', exact: true }).check();
        await expect.poll(drawn).toEqual(images);
        await expect(page.getByRole('checkbox', { name: 'Show Docker image nodes', exact: true })).toBeVisible();
        await expect(page.getByText(`${images.length} nodes`, { exact: true })).toBeVisible();
    });
