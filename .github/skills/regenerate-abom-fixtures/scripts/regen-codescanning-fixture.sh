#!/usr/bin/env bash
# Generates synthetic CodeQL output for the fixture workflow, with no GitHub API requests.
set -euo pipefail

ROOT=$(git rev-parse --show-toplevel)
cd "$ROOT"

CODEQL=${CODEQL:-codeql}
if ! "$CODEQL" version | grep -q 'release 2.27.1'; then
    echo "CodeQL CLI 2.27.1 is required to regenerate this fixture" >&2
    exit 1
fi

TARGET=testdata/codescanning/synthetic
SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/abom-actions-fixture.XXXXXX")
trap 'rm -rf "$SCRATCH"' EXIT

mkdir -p "$TARGET"
SOURCE="$SCRATCH/source"
mkdir -p "$SOURCE"
cp -R testdata/codescanning/source/. "$SOURCE/"
git -C "$SOURCE" init --quiet
git -C "$SOURCE" config user.name 'ABOM Test Fixture'
git -C "$SOURCE" config user.email 'abom-fixture@example.invalid'
git -C "$SOURCE" config commit.gpgsign false
git -C "$SOURCE" add .
GIT_AUTHOR_DATE='2020-01-01T00:00:00Z' GIT_COMMITTER_DATE='2020-01-01T00:00:00Z' \
    git -C "$SOURCE" commit --quiet -m 'Synthetic Actions database fixture'
COMMIT=$(git -C "$SOURCE" rev-parse HEAD)

"$CODEQL" pack download codeql/actions-queries@0.6.36
"$CODEQL" database create --language=actions --source-root="$SOURCE" --overwrite "$SCRATCH/database"
"$CODEQL" database analyze --format=sarif-latest --sarif-group-rules-by-pack \
    --no-sarif-add-file-contents --output="$TARGET/analysis.sarif" "$SCRATCH/database" \
    codeql/actions-queries@0.6.36:codeql-suites/actions-security-extended.qls

mkdir -p "$SCRATCH/archive"
cp -R "$SCRATCH/database" "$SCRATCH/archive/actions"
(cd "$SCRATCH/archive" && zip -qr "$ROOT/$TARGET/database.fixture" actions)

echo "recorded synthetic Actions database and analysis for $COMMIT"
