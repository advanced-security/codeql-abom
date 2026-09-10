package store

import (
	"os"
	"testing"
)

// benchStore opens the corpus named by ABOM_BENCH_DB, or skips.
//
// A benchmark over `newTestStore`'s empty tables measures something nobody runs, which is the
// mistake that made this plan necessary: a `sqlite3` CLI timing on a cold page cache ranked a
// rewrite first, and the served path had never been paying it. A benchmark with no corpus is the
// same error with Go syntax, so this refuses rather than reporting a fast nothing.
//
// Point it at a snapshot, never the live database:
//
//	sqlite3 -readonly "file:$ABOM_DB?immutable=1" ".backup /tmp/abom-bench.db"
//	make bench ABOM_BENCH_DB=/tmp/abom-bench.db
func benchStore(b *testing.B) *Store {
	b.Helper()

	path := os.Getenv("ABOM_BENCH_DB")
	if path == "" {
		b.Skip("ABOM_BENCH_DB is unset, so there is no corpus to measure; " +
			"see plans/tools/performance-measurement.md")
	}
	if _, err := os.Stat(path); err != nil {
		b.Skipf("ABOM_BENCH_DB=%s cannot be read (%v), so nothing was measured", path, err)
	}

	s, err := Open(path, Options{})
	if err != nil {
		b.Fatalf("opening the benchmark corpus: %v", err)
	}
	b.Cleanup(func() { s.Close() })

	// State the corpus. A number that does not say what it ran against cannot be compared to the
	// next one, and every figure in plans/db/data-efficiency.md carries this.
	var nodes, edges, frontier int
	_ = s.DB().QueryRow(`SELECT
		(SELECT COUNT(*) FROM abom_node_live),
		(SELECT COUNT(*) FROM abom_edge_live),
		(SELECT COUNT(*) FROM abom_frontier)`).Scan(&nodes, &edges, &frontier)
	b.Logf("corpus %s: %d nodes, %d edges, %d frontier rows", path, nodes, edges, frontier)

	return s
}

// BenchmarkFrontierSnapshot measures the read the Discovery page waits on.
//
// This is the endpoint whose 0.68 s had no owner: the SQL accounts for roughly 0.15 s of it and
// nothing in the repository could see the rest, because every instrument measured either the
// sqlite3 CLI or the HTTP boundary and the cost sits between them.
func BenchmarkFrontierSnapshot(b *testing.B) {
	s := benchStore(b)
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		items, err := s.FrontierSnapshot("", 500)
		if err != nil {
			b.Fatalf("FrontierSnapshot: %v", err)
		}
		if len(items) == 0 {
			b.Fatal("the corpus returned no frontier rows, so this measured an empty answer")
		}
	}
}

// BenchmarkFullGraph measures the payload the Explorer loads, which ui/performance.md blames for
// 3.4 FPS at 9,811 nodes.
func BenchmarkFullGraph(b *testing.B) {
	s := benchStore(b)
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		g, err := s.FullGraph(FullGraphOptions{})
		if err != nil {
			b.Fatalf("FullGraph: %v", err)
		}
		if len(g.Nodes) == 0 {
			b.Fatal("the corpus returned no graph nodes, so this measured an empty answer")
		}
	}
}
