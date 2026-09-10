import { defineConfig, devices } from '@playwright/test';

// Paths come from the Makefile so `make e2e` and a bare `npx playwright test` agree. Defaults
// exist only so the second one works at all; the Makefile always passes them explicitly.
const port = process.env.ABOM_E2E_PORT ?? '8231';
const bin = process.env.ABOM_E2E_BIN ?? '../../../bin/codeql-abom';
const db = process.env.ABOM_E2E_DB ?? '/tmp/abom-e2e/abom.db';
const ui = process.env.ABOM_E2E_UI ?? '/tmp/abom-ui/wwwroot';

export default defineConfig({
    testDir: './specs',
    // The suite drives one server against one SQLite file, and the store allows a single writer.
    // Parallel workers would be several readers of one connection pool, which is not the thing
    // being tested and produces failures that look like interface bugs.
    workers: 1,
    fullyParallel: false,
    forbidOnly: !!process.env.CI,
    retries: 0,
    reporter: process.env.CI ? [['github'], ['list']] : [['list']],
    timeout: 30_000,
    expect: { timeout: 10_000 },

    use: {
        baseURL: `http://127.0.0.1:${port}`,
        // Blazor WASM boots the runtime before anything renders, so a trace on first failure is
        // worth far more here than a screenshot of a blank page.
        trace: 'retain-on-failure',
        screenshot: 'only-on-failure',
    },

    projects: [
        {
            name: 'chromium',
            use: {
                ...devices['Desktop Chrome'],
                // The full browser rather than the stripped headless shell that is now the
                // default: this suite's claims are about rendering and computed geometry, and
                // they should be measured in the browser a reader actually runs.
                //
                // `chromium` is the pinned build Playwright downloads, and is what CI should
                // use because it is the same everywhere. `ABOM_E2E_CHANNEL=chrome` borrows the
                // system install instead, which is the difference between a usable suite and a
                // blocked one on a machine where that 130 MB download will not complete.
                channel: process.env.ABOM_E2E_CHANNEL ?? 'chromium',
                // Fixed, because every geometry assertion in this suite is stated at a width.
                // A viewport that follows the window makes a wrap regression reproduce on one
                // machine and not another.
                viewport: { width: 1440, height: 900 },
            },
        },
    ],

    webServer: {
        command: `${bin} serve --db ${db} --addr 127.0.0.1:${port} --ui-dir ${ui}`,
        url: `http://127.0.0.1:${port}/api/summary`,
        // Never reuse: a server already on this port is somebody else's, pointed at a database
        // this suite did not prepare, and its answers would be reported as ours.
        reuseExistingServer: false,
        timeout: 30_000,
        stdout: 'pipe',
        stderr: 'pipe',
    },
});
