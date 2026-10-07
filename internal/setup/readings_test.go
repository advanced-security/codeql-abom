package setup

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func TestReadingsCountEveryClassAndSayWhatPutEachRepositoryThere(t *testing.T) {
	s := ownedStore(t, "octo/covered", "octo/enable", "octo/unread")
	for repo, setup := range map[string]catalog.DefaultSetup{
		"octo/covered": {State: "configured", Languages: []string{"actions", "python"}},
		"octo/enable":  {State: "not-configured", Languages: []string{"actions"}},
	} {
		r := Reader{Store: s, Catalog: &fakeCatalog{setup: setup}, Now: func() time.Time { return readAt }}
		if err := r.Read(context.Background(), repo); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB().Exec(`INSERT INTO abom_frontier (node_key, ref, depth, status, enqueued_at)
	                           VALUES ('octo/enable', '', 0, 'done', '2026-10-05T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := (Reader{Store: s, Catalog: &fakeCatalog{}, Now: func() time.Time { return readAt }}).
		ReadConfigurations(context.Background(), "octo"); err != nil {
		t.Fatal(err)
	}

	got, err := Classify(s, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Counts[store.SetupCovered] != 1 || got.Counts[store.SetupEnable] != 1 || got.Counts[store.SetupNotRead] != 1 {
		t.Errorf("counts %v, want one covered, one enable, one not read", got.Counts)
	}
	if got.Declared != 3 || got.Read != 2 || got.OldestRead == "" {
		t.Errorf("coverage: %d declared, %d read, oldest %q; want 3, 2 and a time", got.Declared, got.Read, got.OldestRead)
	}
	for _, item := range got.Items {
		if item.Evidence == "" {
			t.Errorf("%s (%s) carries no evidence", item.Repository, item.Class)
		}
		if item.Writable != (item.Class == store.SetupEnable || item.Class == store.SetupAdd) {
			t.Errorf("%s (%s) writable = %v", item.Repository, item.Class, item.Writable)
		}
		if item.Repository == "octo/covered" && !strings.Contains(item.Evidence, "actions, python") {
			t.Errorf("covered evidence %q does not name the languages read", item.Evidence)
		}
	}

	only, err := Classify(s, store.SetupEnable)
	if err != nil || len(only.Items) != 1 || only.Items[0].Repository != "octo/enable" || only.Counts[store.SetupCovered] != 1 {
		t.Errorf("a class filter narrows the items and keeps every count: %+v %v", only, err)
	}
}
