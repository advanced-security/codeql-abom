package store

import "testing"

func TestAToolPathIsKeptUntilClearedAndNeverInvented(t *testing.T) {
	s := newTestStore(t)

	paths, err := s.ToolPaths()
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("an installation nobody configured names no tools, got %v", paths)
	}

	if err := s.SetToolPath("copilot", "/opt/copilot/copilot", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetToolPath("copilot", "/usr/local/bin/copilot", "bob"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetToolPath("codeql", "/opt/codeql/codeql", "alice"); err != nil {
		t.Fatal(err)
	}
	paths, err = s.ToolPaths()
	if err != nil {
		t.Fatal(err)
	}
	got := paths["copilot"]
	if got.Path != "/usr/local/bin/copilot" || got.SetBy != "bob" || got.SetAt == "" {
		t.Fatalf("the latest path and who set it are kept, got %+v", got)
	}
	if paths["codeql"].Path != "/opt/codeql/codeql" {
		t.Fatalf("each tool keeps its own path, got %+v", paths)
	}

	if err := s.SetToolPath("copilot", "", "bob"); err != nil {
		t.Fatal(err)
	}
	paths, err = s.ToolPaths()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := paths["copilot"]; ok {
		t.Fatalf("clearing a path returns the tool to its default, got %+v", paths)
	}
	if err := s.SetToolPath("node", "/usr/bin/node", "bob"); err == nil {
		t.Fatal("a tool this server does not run is refused")
	}
}
