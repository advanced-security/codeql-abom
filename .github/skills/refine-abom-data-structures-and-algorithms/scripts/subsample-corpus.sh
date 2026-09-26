#!/usr/bin/env bash
# Builds scaled copies of a corpus snapshot so a growth curve can be fitted.
#
# A 10,000-repository corpus cannot be staged here, so scale claims come from measuring several
# sizes and fitting log(cost) against log(size). That only works if the smaller corpora are the
# same *shape* as the large one.
#
# So the sampling preserves closure: a dependency node reached by a surviving workflow survives
# even though its own repository was not sampled. Dropping those instead shrinks the graph faster
# than the repository count and yields a flattering exponent that is purely a sampling artifact.
set -euo pipefail

SRC=${1:?usage: subsample-corpus.sh <snapshot.db> <out-dir> <percent...>}
OUTDIR=${2:?usage: subsample-corpus.sh <snapshot.db> <out-dir> <percent...>}
shift 2
PERCENTS=("$@")
if (( ${#PERCENTS[@]} == 0 )); then
    PERCENTS=(10 25 50 100)
fi

if [[ ! -r "$SRC" ]]; then
    echo "no such database: $SRC" >&2
    exit 1
fi
if [[ -e "$SRC-wal" && -s "$SRC-wal" ]]; then
    echo "$SRC has a non-empty WAL, so it is probably live. Take a .backup snapshot first." >&2
    exit 1
fi

mkdir -p "$OUTDIR"

# immutable=1 rather than mode=ro: a fresh .backup of a WAL database has no -shm file and a
# read-only connection may not create one, so mode=ro fails on a snapshot that is perfectly good.
printf '%-6s %8s %8s %8s %8s %10s %s\n' pct repos nodes edges findings MiB cache
for pct in "${PERCENTS[@]}"; do
    out="$OUTDIR/abom-${pct}pct.db"
    rm -f "$out" "$out-wal" "$out-shm"
    sqlite3 -readonly "file:$SRC?immutable=1" ".backup '$out'"

    # row_id % divisor rather than a LIMIT: repository row ids follow discovery order, so a prefix
    # would be almost entirely seed repositories and the sample would not resemble the corpus.
    if (( pct < 100 )); then
        divisor=$((100 / pct))
        sqlite3 "$out" <<SQL
PRAGMA foreign_keys = OFF;
BEGIN;

CREATE TEMP TABLE keep_repo AS
  SELECT row_id, repository_full_name FROM repository WHERE row_id % $divisor = 0;

DELETE FROM abom_finding_node
 WHERE finding_row_id IN (
   SELECT row_id FROM abom_finding
    WHERE repository_row_id NOT IN (SELECT row_id FROM keep_repo));
DELETE FROM abom_finding
 WHERE repository_row_id NOT IN (SELECT row_id FROM keep_repo);

DELETE FROM abom_edge
 WHERE repository_row_id IS NOT NULL
   AND repository_row_id NOT IN (SELECT row_id FROM keep_repo);

-- The closure half: a node survives if its repository was sampled, or if any surviving edge still
-- touches it. Without the second clause every external dependency disappears with its owner and
-- the sampled graph stops being a smaller version of the same thing.
CREATE TEMP TABLE keep_node AS
  SELECT row_id FROM abom_node
   WHERE repository_row_id IN (SELECT row_id FROM keep_repo)
  UNION
  SELECT src_node_row_id FROM abom_edge
  UNION
  SELECT dst_node_row_id FROM abom_edge;

DELETE FROM abom_finding_node WHERE node_row_id NOT IN (SELECT row_id FROM keep_node);
DELETE FROM abom_node        WHERE row_id      NOT IN (SELECT row_id FROM keep_node);

-- A container pointing at a deleted node would fail the foreign key the application opens with.
UPDATE abom_node SET container_node_row_id = NULL
 WHERE container_node_row_id IS NOT NULL
   AND container_node_row_id NOT IN (SELECT row_id FROM abom_node);

DELETE FROM abom_blast_radius WHERE node_row_id NOT IN (SELECT row_id FROM abom_node);
DELETE FROM abom_surface      WHERE node_row_id NOT IN (SELECT row_id FROM abom_node);
DELETE FROM abom_exposure
 WHERE node_row_id NOT IN (SELECT row_id FROM abom_node)
    OR repository_row_id NOT IN (SELECT row_id FROM keep_repo);

-- These join to a repository by full name rather than by row id. abom_observation plus its
-- autoindex is 42% of the reference database, larger than nodes and edges combined, so leaving it
-- unsampled puts a fixed floor under every byte measurement and flattens the size curve entirely.
DELETE FROM abom_query_run     WHERE repository NOT IN (SELECT repository_full_name FROM keep_repo);
DELETE FROM abom_ingest_ledger WHERE repository NOT IN (SELECT repository_full_name FROM keep_repo);
DELETE FROM abom_commit_content WHERE repository NOT IN (SELECT repository_full_name FROM keep_repo);
DELETE FROM abom_submission    WHERE repository NOT IN (SELECT repository_full_name FROM keep_repo);
DELETE FROM abom_observation      WHERE query_run_id NOT IN (SELECT row_id FROM abom_query_run);
DELETE FROM abom_finding_sighting WHERE query_run_id NOT IN (SELECT row_id FROM abom_query_run)
                                     OR finding_row_id NOT IN (SELECT row_id FROM abom_finding);

-- Nullable, so a run dropped with its repository leaves provenance absent rather than dangling.
UPDATE abom_edge    SET query_run_id = NULL WHERE query_run_id NOT IN (SELECT row_id FROM abom_query_run);
UPDATE abom_finding SET query_run_id = NULL WHERE query_run_id NOT IN (SELECT row_id FROM abom_query_run);

DELETE FROM abom_frontier
 WHERE node_key NOT IN (SELECT node_key FROM abom_node);
DELETE FROM repository WHERE row_id NOT IN (SELECT row_id FROM keep_repo);

COMMIT;
VACUUM;
SQL
    fi

    read -r repos nodes edges findings mib <<<"$(sqlite3 -readonly "file:$out?immutable=1" "
      SELECT (SELECT count(*) FROM repository),
             (SELECT count(*) FROM abom_node_live),
             (SELECT count(*) FROM abom_edge_live),
             (SELECT count(*) FROM abom_finding),
             ROUND((SELECT page_count*page_size/1048576.0
                      FROM pragma_page_count(), pragma_page_size()), 1);" | tr '|' ' ')"

    # 256 MiB is DefaultCacheSizeKB. Below it every read is from memory and every curve looks
    # linear, so a growth measurement that never crosses this line says nothing about life above it.
    cache=$(awk -v m="$mib" 'BEGIN { print (m > 256 ? "ABOVE-CACHE" : "fits") }')
    printf '%-6s %8s %8s %8s %8s %10s %s\n' "$pct" "$repos" "$nodes" "$edges" "$findings" "$mib" "$cache"

    sqlite3 -readonly "file:$out?immutable=1" "PRAGMA integrity_check;" | grep -qx ok \
        || { echo "integrity_check failed for $out" >&2; exit 1; }
done

echo
# A table added to the schema later will not be sampled by anything above, and will sit as a fixed
# floor under every byte measurement without saying so. abom_observation did exactly that, at 42%
# of the database, until this check was written. Let the omission announce itself.
smallest=$(printf '%s\n' "${PERCENTS[@]}" | sort -n | head -1)
small_db="$OUTDIR/abom-${smallest}pct.db"
full_db="$OUTDIR/abom-100pct.db"
if [[ "$smallest" != "100" && -r "$small_db" && -r "$full_db" ]]; then
    if sqlite3 -readonly "file:$full_db?immutable=1" "SELECT 1 FROM dbstat LIMIT 1;" >/dev/null 2>&1; then
        # Two separate connections, joined in awk: dbstat is a virtual table over the main schema
        # and does not scope to an ATTACHed one, so a cross-database comparison in SQL silently
        # compares the sample against itself and reports every large table as unsampled.
        sizes() {
            sqlite3 -readonly "file:$1?immutable=1" \
                "SELECT name, SUM(pgsize) FROM dbstat GROUP BY name;"
        }
        echo "Tables over 0.5 MiB in the ${smallest}% sample that did not shrink with it:"
        { sizes "$full_db" | sed 's/^/full|/'; sizes "$small_db" | sed 's/^/small|/'; } |
            awk -F'|' '
                $1 == "full"  { full[$2] = $3 }
                $1 == "small" { small[$2] = $3 }
                END {
                    n = 0
                    for (t in small)
                        if (small[t] > 524288 && full[t] > 0 && small[t] > 0.8 * full[t]) {
                            printf "  %-40s %6.2f MiB of %6.2f MiB\n", t, small[t]/1048576, full[t]/1048576
                            n++
                        }
                    if (n == 0) print "  none — every large object scales with the sample"
                }'
    else
        echo "dbstat unavailable, so the unsampled-table check could not run."
    fi
fi

echo
echo "Now take the phase 1 census and the phase 2 timings against each, then fit the exponent."
echo "Report the points alongside it: four points over one order of magnitude, extrapolated two,"
echo "is a weak claim and has to be labeled one."
