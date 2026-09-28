package normalize

import "testing"

// The hash exists so two rebuilds of an unchanged corpus can be compared token for token. Nothing
// that varies between runs may enter it: no absolute path, no timestamp, no run id.
func TestTheSameFindingHashesTheSameEveryTime(t *testing.T) {
	first := ResultHash("actions/unpinned-tag", ".github/workflows/main.yml", "9ccc38ab852c9752:1", Region{33, 11, 33, 57})
	second := ResultHash("actions/unpinned-tag", ".github/workflows/main.yml", "9ccc38ab852c9752:1", Region{33, 11, 33, 57})

	if first != second {
		t.Errorf("the same finding hashed %s then %s", first, second)
	}
	if first == "" {
		t.Error("the hash is empty")
	}
}

// The point of the hash: one defect reached through many repositories is a group-by, not a
// traversal. The repository is a column beside the hash, never inside it.
func TestTheSameDefectInTwoRepositoriesSharesAHash(t *testing.T) {
	one := ResultHash("actions/unpinned-tag", ".github/workflows/ci.yml", "7e992c8f2c9966e9:1", Region{12, 9, 12, 40})
	two := ResultHash("actions/unpinned-tag", ".github/workflows/ci.yml", "7e992c8f2c9966e9:1", Region{88, 9, 88, 40})

	if one != two {
		t.Error("the same rule on the same line content hashed differently at a different line")
	}
}

// A finding does not become a different finding because unrelated lines were inserted above it.
// CodeQL's own line-content hash carries the location's identity, so the line number does not
// need to and must not.
func TestADifferentRuleOnTheSameLineIsADifferentFinding(t *testing.T) {
	pinned := ResultHash("actions/unpinned-tag", ".github/workflows/ci.yml", "abc:1", Region{12, 9, 12, 40})
	injection := ResultHash("actions/code-injection", ".github/workflows/ci.yml", "abc:1", Region{12, 9, 12, 40})

	if pinned == injection {
		t.Error("two rules firing on one line collapsed into one hash")
	}
}

func TestTheSameRuleInADifferentFileIsADifferentFinding(t *testing.T) {
	main := ResultHash("actions/unpinned-tag", ".github/workflows/main.yml", "abc:1", Region{12, 9, 12, 40})
	release := ResultHash("actions/unpinned-tag", ".github/workflows/release.yml", "abc:1", Region{12, 9, 12, 40})

	if main == release {
		t.Error("one rule in two files collapsed into one hash")
	}
}

// Without a line hash there is nothing content-addressed about the location left, so the region
// has to stand in for it; silently hashing rule-and-path alone would group unrelated findings.
func TestWithoutALineHashTheRegionIdentifiesTheLocation(t *testing.T) {
	here := ResultHash("actions/unpinned-tag", ".github/workflows/ci.yml", "", Region{12, 9, 12, 40})
	there := ResultHash("actions/unpinned-tag", ".github/workflows/ci.yml", "", Region{88, 9, 88, 40})

	if here == there {
		t.Error("two locations with no line hash collapsed into one")
	}
}

// A finding that has a line hash and one that does not are not comparable, and must not be able
// to collide by construction.
func TestALineHashAndABareRegionCannotCollide(t *testing.T) {
	hashed := ResultHash("actions/unpinned-tag", ".github/workflows/ci.yml", "12:9", Region{})
	regioned := ResultHash("actions/unpinned-tag", ".github/workflows/ci.yml", "", Region{12, 9, 0, 0})

	if hashed == regioned {
		t.Error("a line hash and a region hashed to the same value")
	}
}

func TestPathComparisonIgnoresCase(t *testing.T) {
	lower := ResultHash("actions/unpinned-tag", ".github/workflows/ci.yml", "abc:1", Region{})
	upper := ResultHash("actions/unpinned-tag", ".GitHub/Workflows/CI.yml", "abc:1", Region{})

	if lower != upper {
		t.Error("the same path in different case hashed differently")
	}
}
