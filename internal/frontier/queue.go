// Package frontier controls the durable discovery queue through the storage contract.
package frontier

import (
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

type Item = store.Item
type Key = store.Key
type Resolution = store.Resolution
type Recheck = store.Recheck
type NodeRef = store.NodeRef
type Classification = store.Classification
type DepthProgress = store.DepthProgress
type Stages = store.QueueStages

const (
	StatusPending          = store.StatusPending
	StatusRunning          = store.StatusRunning
	StatusDone             = store.StatusDone
	StatusUnavailable      = store.StatusUnavailable
	StatusNothing          = store.StatusNothing
	DefaultMaxDepth        = store.DefaultMaxDepth
	DefaultContainerWeight = store.DefaultContainerWeight
	DefaultStuckAfter      = store.DefaultStuckAfter
	SupplyChainRuntime     = store.SupplyChainRuntime
	SupplyChainBuild       = store.SupplyChainBuild
	HeadRef                = store.HeadRef
	StageExpedited         = store.StageExpedited
	StageCurrent           = store.StageCurrent
	StageExplore           = store.StageExplore
	StageRefresh           = store.StageRefresh
	StageWaiting           = store.StageWaiting
)

// Queue retains mutable scheduling options, not a database handle.
type Queue struct {
	Store           store.Backend
	MaxDepth        int
	ContainerWeight float64
	StuckAfter      int
	Now             func() time.Time
	Closure         func() ([]string, error)
}

func (q *Queue) storage() store.Frontier {
	return q.Store.Frontier(store.FrontierOptions{
		MaxDepth: q.MaxDepth, ContainerWeight: q.ContainerWeight, StuckAfter: q.StuckAfter,
		Now: q.Now, Closure: q.Closure,
	})
}

func (q *Queue) Enqueue(items []Item) (int, error) { return q.storage().Enqueue(items) }
func (q *Queue) Rescore() error                    { return q.storage().Rescore() }
func (q *Queue) Claim(limit int) ([]Item, error)   { return q.storage().Claim(limit) }
func (q *Queue) ReclaimStale(lease time.Duration) (int, error) {
	return q.storage().ReclaimStale(lease)
}
func (q *Queue) ReclaimOrphaned() (int, error)           { return q.storage().ReclaimOrphaned() }
func (q *Queue) NextAttemptAt() (time.Time, bool, error) { return q.storage().NextAttemptAt() }
func (q *Queue) Complete(k Key, r Resolution) error      { return q.storage().Complete(k, r) }
func (q *Queue) DueForRecheck(ttl time.Duration, limit int) ([]Recheck, error) {
	return q.storage().DueForRecheck(ttl, limit)
}
func (q *Queue) Touch(k Key) error                 { return q.storage().Touch(k) }
func (q *Queue) MarkStale(k Key, sha string) error { return q.storage().MarkStale(k, sha) }
func (q *Queue) ReviewDependencies(k Key, moved bool) error {
	return q.storage().ReviewDependencies(k, moved)
}
func (q *Queue) Release(k Key) error                    { return q.storage().Release(k) }
func (q *Queue) Fail(k Key, reason string) error        { return q.storage().Fail(k, reason) }
func (q *Queue) Unavailable(k Key, reason string) error { return q.storage().Unavailable(k, reason) }
func (q *Queue) Nothing(k Key, ref, sha, reason string) error {
	return q.storage().Nothing(k, ref, sha, reason)
}
func (q *Queue) Stats() (map[string]int, error) { return q.storage().Stats() }
func (q *Queue) AwaitCodeScanning(k Key, commit string, until time.Time) (bool, error) {
	return q.storage().AwaitCodeScanning(k, commit, until)
}
func (q *Queue) CodeScanningAnalyzed(repository, commit string) ([]Key, error) {
	return q.storage().CodeScanningAnalyzed(repository, commit)
}
func (q *Queue) ReleaseCodeScanningHolds(repository string) ([]Key, error) {
	return q.storage().ReleaseCodeScanningHolds(repository)
}
func (q *Queue) Expedite(keys ...string) (int, error)     { return q.storage().Expedite(keys...) }
func (q *Queue) ExpediteRefs(refs []NodeRef) (int, error) { return q.storage().ExpediteRefs(refs) }
func (q *Queue) Reclassify(c Classification) (int, error) { return q.storage().Reclassify(c) }
func (q *Queue) Completeness() ([]DepthProgress, error)   { return q.storage().Completeness() }
func (q *Queue) StuckAfterAttempts() int                  { return q.storage().StuckAfterAttempts() }
func (q *Queue) Drained() (bool, error)                   { return q.storage().Drained() }
func (q *Queue) Stages() (Stages, error)                  { return q.storage().Stages() }
func (q *Queue) Inside() (func(string) bool, error)       { return q.storage().Inside() }

func ShallowestIncomplete(progress []DepthProgress) (int, bool) {
	return store.ShallowestIncomplete(progress)
}
