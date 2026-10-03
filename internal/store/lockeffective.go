package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// RepositoryLock is one commit a lock selects for a repository's use of a node at one ref.
type RepositoryLock struct {
	Commit string `json:"commit"`
	// Consumer is whose lock selects it: the repository itself, or a caller of a composite the
	// repository holds.
	Consumer string `json:"consumer"`
	// Workflows are the consumer's workflows whose entries select it when the lock is the
	// repository's own. Via is the caller's pin of the composite when a caller's lock selects it,
	// which governs the step for that caller only.
	Workflows []string `json:"workflows"`
	Via       string   `json:"via,omitempty"`
}

// lockedBy maps a queue row's (node, commit) to the repositories whose current lock selects it.
func lockedBy(governed []LockGoverned) map[[2]string]map[string]bool {
	out := map[[2]string]map[string]bool{}
	for _, g := range governed {
		key := [2]string{g.NodeKey, strings.ToLower(g.Commit)}
		if out[key] == nil {
			out[key] = map[string]bool{}
		}
		out[key][g.Consumer] = true
	}
	return out
}

// attachLockedBy says, on each queue row, whose current lock selects exactly its commit.
//
// A row at a commit nothing declares is queued because a lock chose it; without this it reads as
// a duplicate of the declared row beside it, used by nobody.
func (s *Store) attachLockedBy(items []FrontierItem) error {
	for i := range items {
		items[i].LockedBy = []string{}
	}
	governed, err := s.LockGovernedEdges()
	if err != nil || len(governed) == 0 {
		return err
	}
	byRow := lockedBy(governed)
	for i := range items {
		consumers := byRow[[2]string{items[i].NodeKey, strings.ToLower(items[i].Ref)}]
		items[i].LockedByCount = len(consumers)
		items[i].LockedBy = sample(consumers)
	}
	return nil
}

// attachRepositoryLocks says, for each repository naming a node at one ref, what its own lock
// selects and which of its uses that covers, and what any caller's lock selects for a composite
// it holds.
func (s *Store) attachRepositoryLocks(out *NodeUsers) error {
	resetRepositoryLockFields(out)
	if err := s.attachLockCoverage(out); err != nil {
		return err
	}
	if err := s.attachVerifications(out); err != nil {
		return err
	}
	governed, err := s.lockGovernedOf(out.Key)
	if err != nil || len(governed) == 0 {
		return err
	}
	collectRepositoryLockSummary(out.Ref, governed).apply(out)
	return nil
}

func resetRepositoryLockFields(out *NodeUsers) {
	for i := range out.Repos {
		out.Repos[i].Locks = []RepositoryLock{}
		out.Repos[i].LockCoverage = []string{}
	}
}

type repositoryLockKey struct{ commit, consumer, via string }

type repositoryLockSummary struct {
	locks   map[string]map[repositoryLockKey]map[string]bool
	ownUses map[string]map[int64]bool
}

func collectRepositoryLockSummary(ref string, governed []LockGoverned) repositoryLockSummary {
	summary := repositoryLockSummary{
		locks:   map[string]map[repositoryLockKey]map[string]bool{},
		ownUses: map[string]map[int64]bool{},
	}
	for _, g := range governed {
		if g.DeclaredRef != ref {
			continue
		}
		k := repositoryLockKey{commit: g.Commit, consumer: g.Consumer}
		if g.Rule == LockRuleCaller {
			k.via = g.Context
		} else {
			if summary.ownUses[g.Repository] == nil {
				summary.ownUses[g.Repository] = map[int64]bool{}
			}
			summary.ownUses[g.Repository][g.EdgeID] = true
		}
		if summary.locks[g.Repository] == nil {
			summary.locks[g.Repository] = map[repositoryLockKey]map[string]bool{}
		}
		if summary.locks[g.Repository][k] == nil {
			summary.locks[g.Repository][k] = map[string]bool{}
		}
		if g.Rule != LockRuleCaller {
			summary.locks[g.Repository][k][g.Context] = true
		}
	}
	return summary
}

func (summary repositoryLockSummary) apply(out *NodeUsers) {
	for i := range out.Repos {
		u := &out.Repos[i]
		u.LockedUses = len(summary.ownUses[u.FullName])
		byKey := summary.locks[u.FullName]
		keys := sortedRepositoryLockKeys(byKey)
		for _, k := range keys {
			u.Locks = append(u.Locks, RepositoryLock{
				Commit: k.commit, Consumer: k.consumer, Via: k.via,
				Workflows: keysInOrder(byKey[k]),
			})
		}
	}
}

func sortedRepositoryLockKeys(byKey map[repositoryLockKey]map[string]bool) []repositoryLockKey {
	keys := make([]repositoryLockKey, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].commit != keys[b].commit {
			return keys[a].commit < keys[b].commit
		}
		if keys[a].consumer != keys[b].consumer {
			return keys[a].consumer < keys[b].consumer
		}
		return keys[a].via < keys[b].via
	})
	return keys
}

// attachLockCoverage says what each listed repository's lock reading at the commit it currently
// resolves to recorded, if anything was read there. Empty is "never read", which is not the same
// as "read, and there is no lock".
func (s *Store) attachLockCoverage(out *NodeUsers) error {
	if len(out.Repos) == 0 {
		return nil
	}
	rows, err := s.db.Query(`
		WITH current_root(repository, commit_sha) AS (` + currentRoots + `)
		SELECT DISTINCT LOWER(c.repository), c.coverage
		FROM abom_lock_coverage c
		JOIN current_root r ON r.repository = LOWER(c.repository)
		                   AND r.commit_sha = LOWER(c.commit_sha)
		WHERE c.workflow_path = ''
		ORDER BY 1, 2`)
	if err != nil {
		return fmt.Errorf("reading lock coverage at current commits: %w", err)
	}
	defer rows.Close()
	coverage := map[string][]string{}
	for rows.Next() {
		var repository, state string
		if err := rows.Scan(&repository, &state); err != nil {
			return fmt.Errorf("reading lock coverage at current commits: %w", err)
		}
		coverage[repository] = append(coverage[repository], state)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading lock coverage at current commits: %w", err)
	}
	for i := range out.Repos {
		if states, ok := coverage[strings.ToLower(out.Repos[i].FullName)]; ok {
			out.Repos[i].LockCoverage = states
		}
	}
	return nil
}

// lockedCommitsByEdgeKey maps each governed declaring line to the commits its locks select.
func (s *Store) lockedCommitsByEdgeKey() (map[string][]string, error) {
	governed, err := s.LockGovernedEdges()
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, g := range governed {
		if !slices.Contains(out[g.EdgeKey], g.Commit) {
			out[g.EdgeKey] = append(out[g.EdgeKey], g.Commit)
		}
	}
	return out, nil
}

// attachRelationshipLocks says, on each listed line, what its current locks select.
func (s *Store) attachRelationshipLocks(items []RelationshipSummary) error {
	for i := range items {
		items[i].Locked = []RelationshipLock{}
	}
	governed, err := s.LockGovernedEdges()
	if err != nil || len(governed) == 0 {
		return err
	}
	byKey := map[string][]RelationshipLock{}
	for _, g := range governed {
		l := RelationshipLock{Commit: g.Commit, Consumer: g.Consumer}
		if g.Rule == LockRuleCaller {
			l.Via = g.Context
		} else {
			l.Workflow = g.Context
		}
		if !slices.Contains(byKey[g.EdgeKey], l) {
			byKey[g.EdgeKey] = append(byKey[g.EdgeKey], l)
		}
	}
	for i := range items {
		if locks, ok := byKey[items[i].EdgeKey]; ok {
			items[i].Locked = locks
		}
	}
	return nil
}

// attachVerifications carries each listed repository's newest verification at a commit it
// currently resolves to.
func (s *Store) attachVerifications(out *NodeUsers) error {
	if len(out.Repos) == 0 {
		return nil
	}
	rows, err := s.db.Query(`
		WITH current_root(repository, commit_sha) AS (` + currentRoots + `)
		SELECT LOWER(v.repository), v.row_id, v.repository, v.commit_sha, v.mode, v.outcome,
		       v.tool_version, v.lockfile_version, v.valid, v.findings, v.error, v.requested_by,
		       v.verified_at
		FROM abom_lock_verification v
		JOIN current_root r ON r.repository = LOWER(v.repository)
		                   AND r.commit_sha = LOWER(v.commit_sha)
		ORDER BY v.verified_at DESC, v.row_id DESC`)
	if err != nil {
		return fmt.Errorf("reading lock verifications at current commits: %w", err)
	}
	defer rows.Close()
	newest := map[string]*VerificationBrief{}
	for rows.Next() {
		var repository string
		var v LockVerification
		var valid sql.NullBool
		var findings, at string
		if err := rows.Scan(&repository, &v.ID, &v.Repository, &v.Commit, &v.Mode, &v.Outcome,
			&v.ToolVersion, &v.LockfileVersion, &valid, &findings, &v.Error, &v.RequestedBy,
			&at); err != nil {
			return fmt.Errorf("reading lock verifications at current commits: %w", err)
		}
		if _, seen := newest[repository]; seen {
			continue
		}
		var list []LockVerificationFinding
		if err := json.Unmarshal([]byte(findings), &list); err != nil {
			return fmt.Errorf("reading the findings of lock verification %d: %w", v.ID, err)
		}
		verified, _ := time.Parse(time.RFC3339Nano, at)
		newest[repository] = &VerificationBrief{Mode: v.Mode, Outcome: v.Outcome,
			Findings: len(list), VerifiedAt: verified}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading lock verifications at current commits: %w", err)
	}
	for i := range out.Repos {
		out.Repos[i].Verification = newest[strings.ToLower(out.Repos[i].FullName)]
	}
	return nil
}

// lockGovernedOf is the governed relation for the edges naming one node.
func (s *Store) lockGovernedOf(key string) ([]LockGoverned, error) {
	all, err := s.LockGovernedEdges()
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, g := range all {
		if g.NodeKey == key {
			out = append(out, g)
		}
	}
	return out, nil
}

// sample is the first usedBySample names of a set, in order. The count travels beside it.
func sample(set map[string]bool) []string {
	names := keysInOrder(set)
	if len(names) > usedBySample {
		names = names[:usedBySample]
	}
	return names
}

func keysInOrder[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
