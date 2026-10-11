package expand

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

func (e *Expander) beginReceipt(b batch, p work.Prepared, opts Options, planned tierPlan) (func(bool) error, bool, error) {
	lease, fenced := e.Store.ActiveLease()
	if !fenced {
		return nil, false, nil
	}
	ids := make([]string, 0, len(b.items))
	for _, item := range b.items {
		if item.ClaimID == "" {
			return nil, false, errors.New("a fenced ingestion attempt needs its durable claim identity")
		}
		ids = append(ids, item.ClaimID)
	}
	if len(ids) == 0 {
		return nil, false, errors.New("a fenced ingestion attempt names at least one claim")
	}
	slices.Sort(ids)
	key, err := json.Marshal(ids)
	if err != nil {
		return nil, false, err
	}
	attemptHash := sha256.Sum256(key)
	attempt := hex.EncodeToString(attemptHash[:])
	digest, err := e.evidenceDigest(b, p, opts, planned)
	if err != nil {
		return nil, false, err
	}
	started, err := e.Store.BeginReceipt(attempt, digest, lease)
	if err != nil || !started {
		return nil, !started && err == nil, err
	}
	return func(complete bool) error {
		if complete {
			return e.Store.CompleteReceipt(attempt, digest, lease)
		}
		return e.Store.AbortReceipt(attempt, lease)
	}, false, nil
}

func (e *Expander) evidenceDigest(b batch, p work.Prepared, opts Options, planned tierPlan) (string, error) {
	query := p.ResultQueryID
	if query == "" {
		query = opts.QueryID
	}
	files := map[string]string{"edges": p.ResultPath, "findings": p.SarifPath, "taken": p.TakenSarifPath}
	for query, path := range p.Extra {
		files["query:"+query] = path
	}
	hashes := map[string]string{}
	for name, path := range files {
		if path == "" {
			continue
		}
		digest, err := digestFile(path)
		if err != nil {
			return "", err
		}
		hashes[name] = digest
	}
	at := b.revision(p, e.sourceFilter())
	runs := map[string]store.QueryRun{"edges": planned.queryRun(query, at)}
	for query := range p.Extra {
		runs["query:"+query] = planned.queryRun(query, at)
	}
	if p.SarifPath != "" || p.TakenSarifPath != "" {
		runs["findings"] = planned.findingsRun(at)
	}
	metadata := struct {
		Runs      map[string]store.QueryRun
		TakenFrom string
		Files     map[string]string
	}{runs, p.TakenFrom, hashes}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("encoding ingestion identity: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func digestFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("reading ingestion evidence: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hashing ingestion evidence: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
