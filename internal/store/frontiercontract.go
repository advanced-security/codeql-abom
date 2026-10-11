package store

import "time"

// Frontier persists queue operations on the store's single writer.
type Frontier interface {
	Enqueue([]Item) (int, error)
	Rescore() error
	Claim(int) ([]Item, error)
	ReclaimStale(time.Duration) (int, error)
	ReclaimOrphaned() (int, error)
	NextAttemptAt() (time.Time, bool, error)
	Complete(Key, Resolution) error
	DueForRecheck(time.Duration, int) ([]Recheck, error)
	Touch(Key) error
	MarkStale(Key, string) error
	ReviewDependencies(Key, bool) error
	Release(Key) error
	Fail(Key, string) error
	Unavailable(Key, string) error
	Nothing(Key, string, string, string) error
	Stats() (map[string]int, error)
	AwaitCodeScanning(Key, string, time.Time) (bool, error)
	CodeScanningAnalyzed(string, string) ([]Key, error)
	ReleaseCodeScanningHolds(string) ([]Key, error)
	Expedite(...string) (int, error)
	ExpediteRefs([]NodeRef) (int, error)
	Reclassify(Classification) (int, error)
	Completeness() ([]DepthProgress, error)
	StuckAfterAttempts() int
	Drained() (bool, error)
	Stages() (QueueStages, error)
	Inside() (func(string) bool, error)
}

func (s *Store) Frontier(opts FrontierOptions) Frontier {
	return &Queue{Store: s, FrontierOptions: opts}
}

var _ Frontier = (*Queue)(nil)
