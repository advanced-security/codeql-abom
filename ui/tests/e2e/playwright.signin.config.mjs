import { defineConfig, devices } from '@playwright/test';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { detect } from './browser.mjs';

// The interface with sign-in on, against the test issuer in infra/tests/oidc-issuer: a separate
// suite, because every page of the main one assumes nobody has to sign in. `make e2e-signin` builds
// both programs and passes their paths.
const here = path.dirname(fileURLToPath(import.meta.url));
const port = process.env.ABOM_E2E_SIGNIN_PORT ?? '8232';
const issuerPort = process.env.ABOM_E2E_ISSUER_PORT ?? '9431';
const bin = process.env.ABOM_E2E_BIN ?? '../../../bin/codeql-abom';
const issuerBin = process.env.ABOM_E2E_ISSUER_BIN ?? '../../../.abom/tmp/oidc-issuer';
const db = process.env.ABOM_E2E_SIGNIN_DB ?? '../../../.abom/e2e-signin/abom.db';
const ui = process.env.ABOM_E2E_UI ?? '../../../.abom/ui/wwwroot';
const issuer = `http://127.0.0.1:${issuerPort}`;
const browser = detect(process.env.ABOM_E2E_CHANNEL);

export default defineConfig({
    testDir: './signin',
    workers: 1,
    fullyParallel: false,
    forbidOnly: !!process.env.CI,
    retries: 0,
    reporter: process.env.CI ? [['github'], ['list']] : [['list']],
    timeout: 45_000,
    expect: { timeout: 10_000 },
    use: {
        baseURL: `http://127.0.0.1:${port}`,
        trace: 'retain-on-failure',
        screenshot: 'only-on-failure',
    },
    projects: [{
        name: 'chromium',
        use: { ...devices['Desktop Chrome'], channel: browser.channel, viewport: { width: 1440, height: 900 } },
    }],
    webServer: [
        {
            // One person per ABOM role the suite needs, each with the Entra app role that grants it.
            command: `${issuerBin} --addr 127.0.0.1:${issuerPort} --issuer ${issuer} --client-id abom ` +
                '--person vera=ABOM.Viewer --person otto=ABOM.Operator --person ada=ABOM.Admin',
            url: `${issuer}/.well-known/openid-configuration`,
            reuseExistingServer: false,
            timeout: 30_000,
            stdout: 'pipe',
            stderr: 'pipe',
        },
        {
            command: `${bin} serve --addr 127.0.0.1:${port} --ui-dir ${ui} --wait-for-coordinator 35s ` +
                `--auth oidc --oidc-issuer ${issuer} --oidc-client-id abom ` +
                `--oidc-redirect-url http://127.0.0.1:${port}/auth/callback`,
            // The test issuer checks no secret; ABOM needs one to be configured all the same.
            env: { ...process.env, ABOM_DB: db, ABOM_OIDC_CLIENT_SECRET: 'not-checked',
                PATH: `${path.join(here, 'stubs')}${path.delimiter}${process.env.PATH ?? ''}` },
            url: `http://127.0.0.1:${port}/healthz`,
            reuseExistingServer: false,
            timeout: 75_000,
            stdout: 'pipe',
            stderr: 'pipe',
        },
    ],
});
