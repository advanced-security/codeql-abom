package runner

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// AcceptTests makes each test's report its expected result, which is what a person approved.
func (r *Runner) AcceptTests(ctx context.Context, tests []string) error {
	if len(tests) == 0 || tests[0] == "" {
		return fmt.Errorf("runner: accepting needs a test")
	}
	release, err := r.Lock.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()

	out, err := exec.CommandContext(ctx, r.codeql(), append([]string{"test", "accept"}, tests...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("codeql test accept %s: %w: %s", strings.Join(tests, ", "), err,
			strings.TrimSpace(string(out)))
	}
	return nil
}
