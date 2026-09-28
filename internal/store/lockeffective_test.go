package store

import (
	"slices"
	"testing"
	"time"
)

// Distinct locks of one tag, plus the tag's current commit.
const (
	cleaveLocked = "e97e2d8cc328f1b50210efc529dca0028893a2d9"
	bitLocked    = "6c977a6ca4077a0ceb28ffbe03f59d46e9ac8772"
	liveV1       = "02cb101ec7c40f2c49e1d9714d64511d8e1b74de"
)

const (
	toolLocked   = "0123456789abcdef0123456789abcdef01234567"
	pythonLocked = "89abcdef0123456789abcdef0123456789abcdef"
	retiredLock  = "fedcba9876543210fedcba9876543210fedcba98"
)

// lockedCorpus covers locked, unlocked, refused and unread repositories plus a locked composite.
func lockedCorpus(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	w := NewWriter(s, 0)
	add := func(n Node) {
		t.Helper()
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode %s: %v", n.Key, err)
		}
	}
	add(Node{Key: "dtolnay/rust-toolchain", Kind: "action", Owner: "dtolnay",
		Repo: "rust-toolchain", IsExternal: true})
	add(Node{Key: "actions/setup-python", Kind: "action", Owner: "actions",
		Repo: "setup-python", IsExternal: true})
	for _, repo := range []string{"octo/cleave", "octo/bitfuckit", "octo/plain", "octo/unlocked",
		"octo/broken"} {
		id, err := s.UpsertRepository(repo, "")
		if err != nil {
			t.Fatalf("UpsertRepository: %v", err)
		}
		workflow := repo + "/.github/workflows/kernel.yml"
		step := "step:" + repo + ":.github/workflows/kernel.yml:build:0"
		add(Node{Key: workflow, Kind: "workflow", RepositoryID: id})
		add(Node{Key: step, Kind: "step", RepositoryID: id, ContainerKey: workflow})
		if err := w.AddEdge(Edge{
			Key: repo + ":kernel:10", SrcKey: step, DstKey: "dtolnay/rust-toolchain",
			Kind: "step-uses-action", RefRaw: "dtolnay/rust-toolchain@v1", RefValue: "v1",
			FilePath: ".github/workflows/kernel.yml", StartLine: 10, RepositoryID: id,
		}); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
	}
	toolID, err := s.UpsertRepository("octo/tool", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	add(Node{Key: "octo/tool", Kind: "composite_action", RepositoryID: toolID})
	add(Node{Key: "step:octo/tool:action.yml::0", Kind: "step", RepositoryID: toolID,
		ContainerKey: "octo/tool"})
	if err := w.AddEdge(Edge{
		Key: "octo/tool:action:7", SrcKey: "step:octo/tool:action.yml::0",
		DstKey: "actions/setup-python", Kind: "composite-step-uses-action",
		RefRaw: "actions/setup-python@v5", RefValue: "v5", FilePath: "action.yml", StartLine: 7,
		RepositoryID: toolID,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	lock := func(repository, commit string, graph LockGraph) {
		t.Helper()
		if err := s.RecordLockReading(repository, commit, testReader,
			[]LockCoverage{{Coverage: "covered", Schema: "v0.0.2"}}, graph); err != nil {
			t.Fatalf("RecordLockReading(%s): %v", repository, err)
		}
	}
	rust := func(digest string) LockDependency {
		return LockDependency{Pin: "dtolnay/rust-toolchain@v1", Owner: "dtolnay",
			Repo: "rust-toolchain", Ref: "v1", Algorithm: "sha1", Digest: digest}
	}
	kernel := []LockWorkflowPin{{WorkflowPath: ".github/workflows/kernel.yml",
		Pin: "dtolnay/rust-toolchain@v1"}}
	lock("octo/cleave", "c-cleave", LockGraph{
		Dependencies: []LockDependency{
			rust(cleaveLocked),
			{Pin: "octo/tool@v1", Owner: "octo", Repo: "tool", Ref: "v1", Algorithm: "sha1",
				Digest: toolLocked},
			{Pin: "actions/setup-python@v5", Owner: "actions", Repo: "setup-python", Ref: "v5",
				Algorithm: "sha1", Digest: pythonLocked},
		},
		Uses:         []LockUse{{Parent: "octo/tool@v1", Child: "actions/setup-python@v5"}},
		WorkflowPins: kernel,
	})
	lock("octo/bitfuckit", "c-bit", LockGraph{
		Dependencies: []LockDependency{rust(bitLocked)}, WorkflowPins: kernel,
	})
	// A lock from a previous commit must answer for nothing now.
	lock("octo/cleave", "c-old", LockGraph{
		Dependencies: []LockDependency{rust(retiredLock)}, WorkflowPins: kernel,
	})
	for repository, coverage := range map[string]string{
		"octo/unlocked": "not_onboarded", "octo/broken": "unsupported",
	} {
		if err := s.RecordLockReading(repository, "c-"+repository[5:], testReader,
			[]LockCoverage{{Coverage: coverage}}, LockGraph{}); err != nil {
			t.Fatalf("RecordLockReading(%s): %v", repository, err)
		}
	}

	for _, f := range []struct{ node, ref, owner, repo, resolved string }{
		{"octo/cleave", "", "octo", "cleave", "c-cleave"},
		{"octo/bitfuckit", "", "octo", "bitfuckit", "c-bit"},
		{"octo/plain", "", "octo", "plain", "c-plain"},
		{"octo/unlocked", "", "octo", "unlocked", "c-unlocked"},
		{"octo/broken", "", "octo", "broken", "c-broken"},
		{"octo/tool", "v1", "octo", "tool", "c-tool"},
		{"dtolnay/rust-toolchain", "v1", "dtolnay", "rust-toolchain", liveV1},
		{"dtolnay/rust-toolchain", cleaveLocked, "dtolnay", "rust-toolchain", cleaveLocked},
		{"dtolnay/rust-toolchain", bitLocked, "dtolnay", "rust-toolchain", bitLocked},
		{"actions/setup-python", pythonLocked, "actions", "setup-python", pythonLocked},
	} {
		if _, err := s.DB().Exec(`
			INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
			                           resolved_sha, analyzed_ref)
			VALUES (?, ?, ?, ?, 1, 'done', '2026-09-27T00:00:00Z', ?, ?)`,
			f.node, f.ref, f.owner, f.repo, f.resolved, f.ref); err != nil {
			t.Fatalf("queueing %s@%s: %v", f.node, f.ref, err)
		}
	}
	return s
}

func frontierRow(t *testing.T, s *Store, node, ref string) FrontierItem {
	t.Helper()
	items, err := s.FrontierSnapshot("", 0)
	if err != nil {
		t.Fatalf("FrontierSnapshot: %v", err)
	}
	for _, it := range items {
		if it.NodeKey == node && it.Ref == ref {
			return it
		}
	}
	t.Fatalf("no queue row %s@%s", node, ref)
	return FrontierItem{}
}

// A row at a commit nothing declares is in the queue because a lock chose it. Without saying
// whose, it reads as a duplicate of the declared row beside it, and its users as nobody.
func TestAQueueRowALockChoseSaysWhoseLockChoseIt(t *testing.T) {
	s := lockedCorpus(t)

	for ref, want := range map[string][]string{
		cleaveLocked: {"octo/cleave"},
		bitLocked:    {"octo/bitfuckit"},
		"v1":         {},
	} {
		row := frontierRow(t, s, "dtolnay/rust-toolchain", ref)
		if !slices.Equal(row.LockedBy, want) || row.LockedByCount != len(want) {
			t.Errorf("@%s locked by %v (%d), want %v", ref, row.LockedBy, row.LockedByCount, want)
		}
	}
	if row := frontierRow(t, s, "dtolnay/rust-toolchain", "v1"); row.UsedByCount != 5 {
		t.Errorf("@v1 used by %d, want the 5 repositories whose source names it", row.UsedByCount)
	}
	caller := frontierRow(t, s, "actions/setup-python", pythonLocked)
	if !slices.Equal(caller.LockedBy, []string{"octo/cleave"}) {
		t.Errorf("a caller's selection is locked by %v, want the caller", caller.LockedBy)
	}
}

// A declared ref says what the source wrote; the locks behind it say what runs. Both, side by
// side, and the declared counts do not move.
func TestADeclaredRefSaysWhichCommitsLocksSelectBehindIt(t *testing.T) {
	s := lockedCorpus(t)

	detail, err := s.NodeDetailOf("dtolnay/rust-toolchain")
	if err != nil {
		t.Fatalf("NodeDetailOf: %v", err)
	}
	if len(detail.Refs) != 1 {
		t.Fatalf("refs = %+v, want v1 alone", detail.Refs)
	}
	v1 := detail.Refs[0]
	if v1.Ref != "v1" || v1.Count != 5 || v1.Repositories != 5 {
		t.Errorf("v1 = %d uses in %d repositories, want the declared 5 and 5", v1.Count, v1.Repositories)
	}
	want := []RefLock{
		{Commit: bitLocked, Consumers: 1, Sample: []string{"octo/bitfuckit"}},
		{Commit: cleaveLocked, Consumers: 1, Sample: []string{"octo/cleave"}},
	}
	if len(v1.Locked) != len(want) {
		t.Fatalf("locked = %+v, want %+v", v1.Locked, want)
	}
	for i := range want {
		if v1.Locked[i].Commit != want[i].Commit || v1.Locked[i].Consumers != want[i].Consumers ||
			!slices.Equal(v1.Locked[i].Sample, want[i].Sample) {
			t.Errorf("locked[%d] = %+v, want %+v", i, v1.Locked[i], want[i])
		}
	}
}

// Each repository naming a version says whether its own lock chose another commit, which of its
// workflows did, and how many of its uses that covers; one with no lock says nothing.
func TestARepositoryNamingAVersionSaysWhatItsLockSelects(t *testing.T) {
	s := lockedCorpus(t)

	users, err := s.NodeUsersOf("dtolnay/rust-toolchain", "v1")
	if err != nil {
		t.Fatalf("NodeUsersOf: %v", err)
	}
	byName := map[string]RepositoryUse{}
	for _, u := range users.Repos {
		byName[u.FullName] = u
	}
	for repo, commit := range map[string]string{"octo/cleave": cleaveLocked, "octo/bitfuckit": bitLocked} {
		u := byName[repo]
		if u.LockedUses != 1 || len(u.Locks) != 1 {
			t.Errorf("%s: %d locked uses, locks %+v; want 1 and one lock", repo, u.LockedUses, u.Locks)
			continue
		}
		l := u.Locks[0]
		if l.Commit != commit || l.Consumer != repo || l.Via != "" ||
			!slices.Equal(l.Workflows, []string{".github/workflows/kernel.yml"}) {
			t.Errorf("%s: lock %+v, want %s by its own kernel.yml", repo, l, commit)
		}
	}
	if plain := byName["octo/plain"]; plain.LockedUses != 0 || len(plain.Locks) != 0 {
		t.Errorf("octo/plain: %+v, want no lock", plain)
	}
}

// Whether a repository's lock was read is its own fact. Never read, read and absent, and read
// and refused are three different answers, and "no lock selects another commit" is true of all
// three while meaning something different each time.
func TestARepositoryNamingAVersionSaysWhetherItsLockWasRead(t *testing.T) {
	s := lockedCorpus(t)

	users, err := s.NodeUsersOf("dtolnay/rust-toolchain", "v1")
	if err != nil {
		t.Fatalf("NodeUsersOf: %v", err)
	}
	got := map[string][]string{}
	for _, u := range users.Repos {
		got[u.FullName] = u.LockCoverage
	}
	for repo, want := range map[string][]string{
		"octo/cleave":    {"covered"},
		"octo/bitfuckit": {"covered"},
		"octo/unlocked":  {"not_onboarded"},
		"octo/broken":    {"unsupported"},
		"octo/plain":     {},
	} {
		if !slices.Equal(got[repo], want) || got[repo] == nil {
			t.Errorf("%s: lock coverage %v, want %v", repo, got[repo], want)
		}
	}
}

// A caller's lock governs a composite's step for that caller only, so it is reported as the
// caller's choice, through the caller's pin, and never as a use the composite itself has locked.
func TestACompositesUseSaysWhichCallersLockSelectsIt(t *testing.T) {
	s := lockedCorpus(t)

	users, err := s.NodeUsersOf("actions/setup-python", "v5")
	if err != nil {
		t.Fatalf("NodeUsersOf: %v", err)
	}
	if len(users.Repos) != 1 || users.Repos[0].FullName != "octo/tool" {
		t.Fatalf("users = %+v, want octo/tool", users.Repos)
	}
	tool := users.Repos[0]
	if tool.LockedUses != 0 {
		t.Errorf("locked uses = %d, want 0: its own lock chose nothing", tool.LockedUses)
	}
	want := RepositoryLock{Commit: pythonLocked, Consumer: "octo/cleave", Via: "octo/tool@v1",
		Workflows: []string{}}
	if len(tool.Locks) != 1 || tool.Locks[0].Commit != want.Commit ||
		tool.Locks[0].Consumer != want.Consumer || tool.Locks[0].Via != want.Via {
		t.Errorf("locks = %+v, want %+v", tool.Locks, want)
	}
}

// A lock its repository has moved past selects nothing on any reader, the same as for discovery.
func TestALockItsRepositoryMovedPastAppearsNowhere(t *testing.T) {
	s := lockedCorpus(t)

	detail, err := s.NodeDetailOf("dtolnay/rust-toolchain")
	if err != nil {
		t.Fatalf("NodeDetailOf: %v", err)
	}
	for _, r := range detail.Refs {
		for _, l := range r.Locked {
			if l.Commit == retiredLock {
				t.Errorf("the retired lock's commit is reported behind %s", r.Ref)
			}
		}
	}
	users, err := s.NodeUsersOf("dtolnay/rust-toolchain", "v1")
	if err != nil {
		t.Fatalf("NodeUsersOf: %v", err)
	}
	for _, u := range users.Repos {
		for _, l := range u.Locks {
			if l.Commit == retiredLock {
				t.Errorf("%s reports the retired lock's commit", u.FullName)
			}
		}
	}
}

// Empty lock lists must be non-nil because the interface iterates them.
func TestAnUnlockedCorpusReportsEmptyLocksRatherThanNone(t *testing.T) {
	s := newTestStore(t)
	id, err := s.UpsertRepository("octo/plain", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	w := NewWriter(s, 0)
	for _, n := range []Node{
		{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout", IsExternal: true},
		{Key: "octo/plain/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: id},
		{Key: "step:octo/plain:ci:0", Kind: "step", RepositoryID: id,
			ContainerKey: "octo/plain/.github/workflows/ci.yml"},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	if err := w.AddEdge(Edge{Key: "plain:ci:3", SrcKey: "step:octo/plain:ci:0",
		DstKey: "actions/checkout", Kind: "step-uses-action", RefValue: "v4",
		FilePath: ".github/workflows/ci.yml", StartLine: 3, RepositoryID: id}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	detail, err := s.NodeDetailOf("actions/checkout")
	if err != nil {
		t.Fatalf("NodeDetailOf: %v", err)
	}
	for _, r := range detail.Refs {
		if r.Locked == nil {
			t.Errorf("%s: locked is nil", r.Ref)
		}
	}
	users, err := s.NodeUsersOf("actions/checkout", "v4")
	if err != nil {
		t.Fatalf("NodeUsersOf: %v", err)
	}
	for _, u := range users.Repos {
		if u.Locks == nil {
			t.Errorf("%s: locks is nil", u.FullName)
		}
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at)
		VALUES ('actions/checkout', 'v4', 'actions', 'checkout', 1, 'pending', '2026-09-27T00:00:00Z')`); err != nil {
		t.Fatalf("queueing: %v", err)
	}
	if row := frontierRow(t, s, "actions/checkout", "v4"); row.LockedBy == nil {
		t.Error("the queue row's locked-by list is nil")
	}
}

// A bill of materials names each reference as written and, beside it, the commits the locks of
// the repositories it is about select — only theirs. A lock of a repository outside the document
// describes somebody else's supply chain.
func TestABillOfMaterialsSaysWhatItsRootsLocksSelect(t *testing.T) {
	s := lockedCorpus(t)

	for name, c := range map[string]struct {
		roots []string
		want  map[string][]string
	}{
		"one root": {[]string{"octo/cleave"}, map[string][]string{cleaveLocked: {"octo/cleave"}}},
		"two roots": {[]string{"octo/cleave", "octo/bitfuckit"}, map[string][]string{
			cleaveLocked: {"octo/cleave"}, bitLocked: {"octo/bitfuckit"}}},
		"unlocked root": {[]string{"octo/plain"}, map[string][]string{}},
	} {
		bom, err := s.BillOfMaterials(BOMOptions{Roots: c.roots})
		if err != nil {
			t.Fatalf("%s: BillOfMaterials: %v", name, err)
		}
		var ref *BOMReference
		for i := range bom.Components {
			if bom.Components[i].Key != "dtolnay/rust-toolchain" {
				continue
			}
			for j := range bom.Components[i].References {
				if bom.Components[i].References[j].Value == "v1" {
					ref = &bom.Components[i].References[j]
				}
			}
		}
		if ref == nil {
			t.Fatalf("%s: no v1 reference to rust-toolchain in %+v", name, bom.Components)
		}
		if ref.Locked == nil {
			t.Fatalf("%s: locked is nil", name)
		}
		got := map[string][]string{}
		for _, l := range ref.Locked {
			got[l.Commit] = l.Consumers
			if !l.Analyzed {
				t.Errorf("%s: %s reads as unanalyzed; its queue row is done", name, l.Commit)
			}
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: locked %v, want %v", name, got, c.want)
		}
		for commit, consumers := range c.want {
			if !slices.Equal(got[commit], consumers) {
				t.Errorf("%s: %s locked by %v, want %v", name, commit, got[commit], consumers)
			}
		}
	}
}

// A relationship says what its declaring line's current lock selects: the commit, whose lock, and
// through which workflow. The line itself is the edge, so this is the one place the choice for one
// step of one workflow is stated exactly.
func TestARelationshipSaysWhatALockSelectsForIt(t *testing.T) {
	s := lockedCorpus(t)

	page, err := s.RelationshipsPage(RelationshipsQuery{Limit: 100})
	if err != nil {
		t.Fatalf("RelationshipsPage: %v", err)
	}
	byKey := map[string]RelationshipSummary{}
	for _, r := range page.Items {
		byKey[r.EdgeKey] = r
		if r.Locked == nil {
			t.Errorf("%s: locked is nil", r.EdgeKey)
		}
	}
	cleave := byKey["octo/cleave:kernel:10"]
	if len(cleave.Locked) != 1 || cleave.Locked[0].Commit != cleaveLocked ||
		cleave.Locked[0].Consumer != "octo/cleave" ||
		cleave.Locked[0].Workflow != ".github/workflows/kernel.yml" {
		t.Errorf("octo/cleave's step is locked %+v, want %s by its own kernel.yml", cleave.Locked, cleaveLocked)
	}
	if plain := byKey["octo/plain:kernel:10"]; len(plain.Locked) != 0 {
		t.Errorf("octo/plain's step is locked %+v, want nothing", plain.Locked)
	}
	tool := byKey["octo/tool:action:7"]
	if len(tool.Locked) != 1 || tool.Locked[0].Consumer != "octo/cleave" ||
		tool.Locked[0].Via != "octo/tool@v1" || tool.Locked[0].Commit != pythonLocked {
		t.Errorf("octo/tool's step is locked %+v, want octo/cleave's choice through octo/tool@v1", tool.Locked)
	}
}

// A drawn line stands for every relationship between its two ends, and carries every commit their
// locks select, so a version node drawn by the reference as written still says what runs.
func TestAGraphEdgeCarriesTheCommitsLocksSelectForIt(t *testing.T) {
	s := lockedCorpus(t)

	graph, err := s.FullGraph(FullGraphOptions{IncludeSteps: true})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	found := false
	for _, e := range graph.Edges {
		if e.Locked == nil {
			t.Errorf("%s -> %s: locked is nil", e.Source, e.Target)
		}
		if slices.Contains(e.Keys, "octo/cleave:kernel:10") {
			found = true
			if !slices.Equal(e.Locked, []string{cleaveLocked}) {
				t.Errorf("cleave's line carries %v, want %s", e.Locked, cleaveLocked)
			}
		}
		if slices.Contains(e.Keys, "octo/plain:kernel:10") && len(e.Locked) != 0 {
			t.Errorf("plain's line carries %v, want nothing", e.Locked)
		}
	}
	if !found {
		t.Fatal("no drawn line stands for octo/cleave's step")
	}
}

// Each repository naming a version carries its newest verification at the commit it resolves to
// now, so the tool's answer reads beside ABOM's own reading of the lock. A verification of an
// older commit describes a lock that no longer runs, and is not carried.
func TestARepositoryNamingAVersionCarriesItsNewestVerification(t *testing.T) {
	s := lockedCorpus(t)
	for _, v := range []LockVerification{
		{Repository: "octo/cleave", Commit: "c-old", Mode: "network", Outcome: "passed",
			VerifiedAt: time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)},
		{Repository: "octo/cleave", Commit: "c-cleave", Mode: "local", Outcome: "passed",
			VerifiedAt: time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)},
		{Repository: "octo/cleave", Commit: "c-cleave", Mode: "network", Outcome: "failed",
			Findings: []LockVerificationFinding{{Category: "ref-changed", Severity: "error", Known: true},
				{Category: "ref-moved", Severity: "warning", Known: true}},
			VerifiedAt: time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC)},
	} {
		if _, err := s.RecordLockVerification(v); err != nil {
			t.Fatalf("RecordLockVerification: %v", err)
		}
	}

	users, err := s.NodeUsersOf("dtolnay/rust-toolchain", "v1")
	if err != nil {
		t.Fatalf("NodeUsersOf: %v", err)
	}
	for _, u := range users.Repos {
		switch u.FullName {
		case "octo/cleave":
			v := u.Verification
			if v == nil || v.Mode != "network" || v.Outcome != "failed" || v.Findings != 2 ||
				!v.VerifiedAt.Equal(time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC)) {
				t.Errorf("octo/cleave verification %+v, want the network run at its current commit", v)
			}
		default:
			if u.Verification != nil {
				t.Errorf("%s carries %+v, want none: nothing verified it", u.FullName, u.Verification)
			}
		}
	}
}
