package ghapp

import "testing"

func TestNetworkDatabaseDoesNotInventALocalAppDirectory(t *testing.T) {
	if got := DefaultDir("postgresql://operator:password@host/abom"); got != "" {
		t.Fatalf("a network database has no directory beside it: %q", got)
	}
	if Configured("") {
		t.Fatal("an absent App directory must not inspect the working directory")
	}
}
