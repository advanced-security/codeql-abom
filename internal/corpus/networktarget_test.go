package corpus

import (
	"os"
	"testing"
)

func TestNetworkCorpusTargetNeverCreatesAURLShapedDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := resetCorpusDatabase("postgresql://operator:password@host/abom"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(".")
	if err != nil || len(entries) != 0 {
		t.Fatalf("network target touched the filesystem: %v, %v", entries, err)
	}
}
