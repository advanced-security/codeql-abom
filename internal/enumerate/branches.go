package enumerate

import "context"

// rememberBranches records what each repository a listing named protects, alongside the default
// branch the listing carried, so the live set reads protected branches without anybody running a
// command.
//
// Only repositories the declaration owns are asked: a protected branch is live because the
// operator's workflows run there with secrets, and nobody else's are. One request each, so a sweep
// asks only what moved and a full listing asks everything once a period. A read that fails records
// nothing, and the repository keeps the answer it had: not asked is not unprotected.
func (o Options) rememberBranches(ctx context.Context, listed []string, one *AccountEnumeration) error {
	if o.Branches == nil || len(listed) == 0 {
		return nil
	}
	owned, err := o.Store.OwnedRepositories()
	if err != nil {
		return err
	}
	ours := make(map[string]bool, len(owned))
	for _, name := range owned {
		ours[name] = true
	}
	for _, repository := range listed {
		if !ours[repository] {
			continue
		}
		branches, err := o.Branches(ctx, repository)
		if err != nil {
			one.BranchesUnread++
			if one.BranchesUnreadWhy == "" {
				one.BranchesUnreadWhy = repository + ": " + err.Error()
			}
			continue
		}
		queue, err := o.Store.SetProtectedBranches(repository, branches)
		if err != nil {
			return err
		}
		one.BranchesRead++
		one.ProtectedBranches += len(branches)
		one.BranchesQueued += queue.Queued
		one.BranchesDequeued += queue.Removed
	}
	return nil
}
