#!/usr/bin/env bash
# Dumps EXPLAIN QUERY PLAN for a directory of .sql files and flags the shapes that cost money.
#
# The SQL is not kept in this skill on purpose: a copy of a query in internal/store drifts from the
# original silently, and a plan read from a stale copy is worse than no plan. Extract the queries
# per review and drop them here as one .sql file each, keeping their ? placeholders — the sqlite3
# CLI leaves unbound parameters as NULL, which does not change the plan.
set -euo pipefail

DB=${1:?usage: query-plans.sh <snapshot.db> <sql-dir>}
SQLDIR=${2:?usage: query-plans.sh <snapshot.db> <sql-dir>}

# immutable=1 rather than mode=ro: a fresh .backup of a WAL database has no -shm file, a read-only
# connection may not create one, and mode=ro therefore fails on a snapshot that is perfectly good.
if [[ ! -r "$DB" ]]; then
    echo "no such database: $DB" >&2
    exit 1
fi

# Refuse a live database. Opening the real one read-only is still a connection the server does not
# expect, and the whole protocol is built on measuring a .backup snapshot.
if [[ -e "$DB-wal" && -s "$DB-wal" ]]; then
    echo "$DB has a non-empty WAL, so it is probably live. Take a .backup snapshot first." >&2
    exit 1
fi

shopt -s nullglob
files=("$SQLDIR"/*.sql)
if (( ${#files[@]} == 0 )); then
    echo "no .sql files in $SQLDIR — extract the read paths from internal/store first" >&2
    exit 1
fi

stats=$(sqlite3 -readonly "file:$DB?immutable=1" \
    "SELECT count(*) FROM sqlite_master WHERE name='sqlite_stat1';")
if [[ "$stats" == "0" ]]; then
    echo "WARNING: no sqlite_stat1 — SQLite is planning on defaults, not on this corpus."
    echo "         ANALYZE runs only inside compaction. Measure these plans again after"
    echo "         running ANALYZE on a writable copy, and report the two sets separately."
    echo
fi

flagged=0
for f in "${files[@]}"; do
    echo "=== $(basename "$f") ==="
    plan=$(sqlite3 -readonly "file:$DB?immutable=1" ".mode list" "EXPLAIN QUERY PLAN $(cat "$f")" 2>&1) || {
        echo "  could not plan this file:"
        echo "$plan" | sed 's/^/  /'
        echo
        continue
    }
    echo "$plan" | sed 's/^/  /'

    while IFS= read -r line; do
        case "$line" in
            (*"CORRELATED SCALAR SUBQUERY"*)
                echo "  >> CORRELATED SUBQUERY — evaluated per outer row. This is the 393x shape."
                flagged=$((flagged + 1)) ;;
            (*SCAN*)
                echo "  >> SCAN — full pass over $(echo "$line" | sed 's/.*SCAN //; s/ .*//')."
                flagged=$((flagged + 1)) ;;
            (*"USE TEMP B-TREE"*)
                echo "  >> TEMP B-TREE — the sort is not served by an index."
                flagged=$((flagged + 1)) ;;
        esac
    done <<< "$plan"
    echo
done

echo "flagged $flagged line(s) across ${#files[@]} quer(y|ies)."
echo
echo "A SCAN is not automatically wrong: scanning a small table once beats an index lookup."
echo "A SCAN on the INNER side of a join is the one that costs, because it runs per outer row."
