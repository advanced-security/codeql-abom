package expand

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
)

// agreement is one retained checkout set beside GitHub's listing of the same commit.
type agreement struct {
	repository, commit, filter string
	outcome                    string
	checkout, listing          string
	detail                     string
	attributes                 bool
}

// The listing digest settles a commit only because it equals what a checkout of that commit
// digests to. That is a claim about git, GitHub and this package together, which no fixture can
// make, so it is measured over every checkout the crawl retained. Reads only.
func TestRetainedCheckoutsDigestLikeTheirCommitsListing(t *testing.T) {
	root := os.Getenv("ABOM_AGREEMENT_CHECKOUTS")
	if root == "" {
		t.Skip("set ABOM_AGREEMENT_CHECKOUTS to a checkout cache to compare it with GitHub's listings")
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token == "" {
		t.Fatal("set GITHUB_TOKEN: the anonymous budget cannot list a corpus")
	}
	api := os.Getenv("ABOM_AGREEMENT_API")
	if api == "" {
		api = "https://api.github.com"
	}
	client := catalog.New(api, token, nil)
	trees := GitHubTrees{Client: client}
	scratch := t.TempDir()

	markers, err := filepath.Glob(filepath.Join(root, "*", "*", "*", cacheMarker))
	if err != nil {
		t.Fatalf("listing %s: %v", root, err)
	}
	if len(markers) == 0 {
		t.Fatalf("%s holds no retained checkouts, so this proves nothing", root)
	}

	results := make([]agreement, len(markers))
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	for i, marker := range markers {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			results[i] = compareRetained(context.Background(), trees, filepath.Dir(marker),
				filepath.Join(scratch, fmt.Sprint(i)))
		}()
	}
	wg.Wait()

	counts := map[string]int{}
	attributes := 0
	for _, r := range results {
		counts[r.outcome]++
		if r.attributes {
			attributes++
		}
		if r.outcome == "edited" {
			t.Logf("%s@%s was edited after it was fetched; a fresh checkout agrees with the listing: %s",
				r.repository, r.commit, r.detail)
		}
		if r.outcome == "disagree" || r.outcome == "outside" || r.outcome == "unreadable" {
			t.Errorf("%s@%s %s: checkout %q, listing %q; %s",
				r.repository, r.commit, r.outcome, r.checkout, r.listing, r.detail)
		}
	}
	names := make([]string, 0, len(counts))
	for k := range counts {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		t.Logf("%-12s %d", k, counts[k])
	}
	t.Logf("%-12s %d", "checkouts", len(results))
	t.Logf("%-12s %d", "requests", client.Stats().Requests)
	t.Logf("%-12s %d listings hold a .gitattributes the digest does not count", "attributes", attributes)

	if out := os.Getenv("ABOM_AGREEMENT_OUT"); out != "" {
		var b strings.Builder
		for _, r := range results {
			fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\n",
				r.repository, r.commit, r.filter, r.outcome, r.checkout, r.listing)
		}
		if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
			t.Fatalf("writing %s: %v", out, err)
		}
	}
}

func compareRetained(ctx context.Context, trees TreeReader, entry, scratch string) agreement {
	rec, err := readRecord(entry)
	if err != nil {
		return agreement{repository: entry, outcome: "unreadable", detail: err.Error()}
	}
	r := agreement{
		repository: rec.Owner + "/" + rec.Repo, commit: rec.Commit, filter: filterID(rec.Patterns),
	}
	filter := parseFilter(rec.Patterns)
	held, outside, err := checkoutEntries(stagedTree(entry), filter)
	if err != nil {
		r.outcome, r.detail = "unreadable", err.Error()
		return r
	}
	if r.checkout, err = CheckoutDigest(stagedTree(entry), rec.Patterns); err != nil {
		r.outcome, r.detail = "unreadable", err.Error()
		return r
	}
	listing, err := trees.ReadTree(ctx, rec.Owner, rec.Repo, rec.Commit)
	if err != nil {
		r.outcome, r.detail = "read-failed", err.Error()
		return r
	}
	r.listing = ListingDigest(listing, rec.Patterns)
	for _, e := range listing.Entries {
		if e.Type == "blob" && strings.HasSuffix("/"+e.Path, "/.gitattributes") && !filter.counts(e.Path) {
			r.attributes = true
		}
	}
	switch {
	case listing.Truncated:
		r.outcome = "truncated"
	case len(outside) > 0:
		r.outcome, r.detail = "outside", "git checked out "+strings.Join(outside, ", ")
	case r.checkout == r.listing && r.checkout == "":
		r.outcome = "both-empty"
	case r.checkout == r.listing:
		r.outcome = "agree"
	default:
		r.outcome, r.detail = "disagree", differences(held, listing, filter)
		// A retained tree somebody edited is not its commit. Only git's own word that it was
		// edited, and a fresh checkout agreeing, reclassify it.
		if edited(stagedTree(entry)) {
			fresh, err := (&Checkout{}).Fetch(ctx, scratch, rec.Owner, rec.Repo, rec.Commit)
			if err == nil {
				digest, err := CheckoutDigest(fresh.Dir, rec.Patterns)
				if err == nil && digest == r.listing {
					r.outcome = "edited"
				}
			}
		}
	}
	return r
}

// edited reports whether git says a checkout's working tree no longer matches its commit.
func edited(dir string) bool {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	// Reading only: status would otherwise refresh the retained checkout's index.
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	return err == nil && len(bytes.TrimSpace(out)) > 0
}

// differences names the paths on which a checkout and a listing part company.
func differences(held []contentEntry, listing Listing, filter pathFilter) string {
	fromCheckout := map[string]contentEntry{}
	for _, e := range held {
		fromCheckout[e.path] = e
	}
	var out []string
	listed := map[string]bool{}
	for _, e := range listing.Entries {
		if e.Type != "blob" || !filter.counts(e.Path) {
			continue
		}
		listed[e.Path] = true
		c, ok := fromCheckout[e.Path]
		switch {
		case !ok:
			out = append(out, "only listed: "+e.Path)
		case c.blob != strings.ToLower(e.SHA) || c.link != (e.Mode == modeLink):
			out = append(out, fmt.Sprintf("differs: %s (checkout %s link=%v, listing %s mode %s)",
				e.Path, c.blob, c.link, e.SHA, e.Mode))
		}
	}
	for path := range fromCheckout {
		if !listed[path] {
			out = append(out, "only checked out: "+path)
		}
	}
	sort.Strings(out)
	return strings.Join(out, "; ")
}
