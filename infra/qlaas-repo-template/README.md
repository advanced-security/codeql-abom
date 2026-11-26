# Query export repository template

ABOM exports a custom query by opening a pull request in one repository, through a second GitHub
App that may write there and nowhere else. You review and merge it. This repository's own
workflows then test it and, on a release, publish the compiled pack. ABOM never merges, tags or
publishes anything.

## What ABOM writes

Each export is one commit on a branch of its own (`abom/export/<query>-<bundle>`), holding:

- every file that makes the query run and be tested, as ABOM read them: the pack manifest and
  lock, the suite, the query, the libraries it imports, its help, and its tests. ABOM leaves out a
  test's last output (`.actual`).
- `abom-exports/<query>-<bundle>.json`, the record of the export. It holds each file's SHA-256,
  the bundle digest, and the digest of the signed decision that sent it, which is in ABOM's
  authorization log.

The pull request's description cites the same decision, the bundle digest, and the newest test
run of that query version.

## Set it up

1. Create a repository for the queries, and copy into it:
   - `codeql-workspace.yml` to its root;
   - `workflows/test.yml` and `workflows/publish.yml` to `.github/workflows/`.
2. Lock the workflows' actions: `gh actions-lock`, then commit `.github/workflows/actions.lock`.
3. Write the CodeQL version `codeql-abom version` names, with a leading `v`, to `.codeql-version`,
   and keep it there as ABOM upgrades. Each pull request names the version its tests last passed
   under.
4. Create the publisher App beside ABOM's database, and install it on this repository only:

   ```bash
   codeql-abom app create --publisher --owner <org> --db <abom.db>
   codeql-abom app status --publisher --db <abom.db>
   ```

   The App asks for contents and pull requests write and metadata read, with no events.
   `app status --publisher` names the repository exports go to. It refuses an App installed on
   all repositories, on more than one, or holding any other permission. Serve refuses to start
   with such an App, rather than hold a write it must not.
5. Restart `codeql-abom serve`. It finds the App in `github-app-publisher` beside `--db`, or in
   `--publisher-app-dir`.

## Export, review, publish

- In ABOM, open a query's components and choose **Open a pull request**.
- Review the pull request. `Test queries` runs its tests with the CLI in `.codeql-version`.
- Merge it, then publish a release. `Publish queries` tests the tagged tree again, compiles the
  pack, and attaches `queries.tgz` to the release.
- Use the queries in code scanning:
  - reference them from a CodeQL configuration, as `queries: - uses: <org>/<repo>/src@<tag>`;
  - or, to publish the pack to the container registry instead, give it a scope your organization
    owns and run `codeql pack publish`.
