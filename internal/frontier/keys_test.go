package frontier

// At names a dependency at a reference.
func At(nodeKey, ref string) Key { return Key{NodeKey: nodeKey, Ref: ref} }

// Of names a dependency at its default branch.
func Of(nodeKey string) Key { return Key{NodeKey: nodeKey} }
