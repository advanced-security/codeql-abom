// The per-element instance registry.
//
// Its own module so that a helper needing an instance does not have to import the entry
// point that imports it back.

export const instances = new Map();
