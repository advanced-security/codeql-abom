package store

// CompositePin is a composite action a workflow pins at a ref nothing has analyzed.
type CompositePin struct {
	NodeKey string
	Owner   string
	Repo    string
	Subpath string
	Ref     string
	// Pins counts the workflows referencing it, so the queue can prefer the widely used ones.
	Pins int
	// PinnedBy is one repository that pins it, kept as provenance for the queued row: a row whose
	// reason nobody recorded cannot be explained later.
	PinnedBy string
}

// UnanalyzedCompositePins reports the composite actions pinned by the operator's own workflows at
// refs no analysis has covered.
//
// This is the ceiling on transitive discovery. A child edge is only visible when the parent was
// analyzed at the ref the consumer pinned, so an unanalyzed pin is coverage ABOM must name.
//
// Restricted to composite actions because nothing else can have children, and queueing the rest
// would spend the crawl on work that cannot produce a transitive edge.
func (s *Store) UnanalyzedCompositePins() ([]CompositePin, error) {
	rows, err := s.DB().Query(`
		SELECT n.node_key, COALESCE(n.owner,''), COALESCE(n.repo,''), COALESCE(n.path,''),
		       COALESCE(e.ref_value,''), COUNT(*) AS pins, MIN(r.repository_full_name)
		  FROM abom_edge_regular e
		  JOIN repository r ON r.row_id = e.repository_row_id AND r.origin = 'seed'
		  JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
		 WHERE n.is_external = 1
		   AND e.file_path LIKE '.github/workflows/%'
		   AND EXISTS (SELECT 1 FROM abom_node_live st
		                WHERE st.container_node_row_id = n.row_id)
		   AND NOT EXISTS (SELECT 1 FROM abom_frontier f
		                    WHERE f.node_key = n.node_key
		                      AND f.ref = COALESCE(e.ref_value,'')
		                      AND f.status = 'done')
		 GROUP BY n.node_key, n.owner, n.repo, n.path, e.ref_value
		 ORDER BY pins DESC, n.node_key, e.ref_value`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CompositePin
	for rows.Next() {
		var p CompositePin
		if err := rows.Scan(&p.NodeKey, &p.Owner, &p.Repo, &p.Subpath, &p.Ref, &p.Pins,
			&p.PinnedBy); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
