package runner

import (
	"context"
	"fmt"
	"os"
)

// Decode writes the JSON form of a BQRS file to jsonPath.
//
// This is the call a crawl makes most: once per graph query per repository, against roughly 0.7s
// of JVM start each as its own process. Through the server it is a few milliseconds.
//
// The answer is held in memory rather than streamed, which the standalone `bqrs.DecodeToJSON`
// avoids. That is affordable here and only here: the graph queries decode a few kilobytes per
// repository, the largest seen across the corpus being 5.3 KB. A query with a result set worth
// streaming should keep using the standalone form.
func (r *Runner) Decode(ctx context.Context, bqrsPath, jsonPath string) error {
	out, err := r.output(ctx, "bqrs", "decode", "--format=json", "--entities=all", bqrsPath)
	if err != nil {
		return fmt.Errorf("decoding %s: %w", bqrsPath, err)
	}
	if err := os.WriteFile(jsonPath, out, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", jsonPath, err)
	}
	return nil
}
