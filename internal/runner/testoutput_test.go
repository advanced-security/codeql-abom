package runner

// parseTestOutput reads the CLI's per-test JSON, without the output files it names.
func parseTestOutput(out []byte) (TestResult, error) {
	outcomes, err := decodeOutcomes(out)
	if err != nil {
		return TestResult{}, err
	}
	return verdict(outcomes, nil), nil
}
