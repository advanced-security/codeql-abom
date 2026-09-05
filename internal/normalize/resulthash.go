package normalize

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Region is a source span. Zero values mean the SARIF omitted the field.
type Region struct {
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
}

// ResultHash identifies a finding by its content, independent of the run and the repository that
// produced it.
//
// Two things follow from that. A rebuild of an unchanged corpus produces the same hashes, which
// is the only real test of "verifiable". And one defect reached through many repositories is a
// group-by rather than a traversal, which is why the repository is a column beside this value and
// never inside it.
//
// The line number is deliberately absent: a finding does not become a different finding because
// unrelated lines were inserted above it, and CodeQL's primaryLocationLineHash already carries
// the location's identity as content. Without that hash there is nothing content-addressed left,
// so the region stands in for it under a different tag, which is what stops the two forms
// colliding.
func ResultHash(ruleID, path, lineHash string, region Region) string {
	h := sha256.New()
	write := func(tag, value string) {
		fmt.Fprintf(h, "%s:%d:%s\x00", tag, len(value), value)
	}
	write("rule", strings.ToLower(ruleID))
	write("path", strings.ToLower(strings.TrimPrefix(path, "/")))

	if lineHash != "" {
		write("line", lineHash)
	} else {
		write("region", fmt.Sprintf("%d:%d:%d:%d",
			region.StartLine, region.StartColumn, region.EndLine, region.EndColumn))
	}
	return hex.EncodeToString(h.Sum(nil))
}
