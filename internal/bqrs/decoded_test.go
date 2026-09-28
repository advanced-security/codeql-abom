package bqrs

import (
	"encoding/json"
	"fmt"
)

// Decoded maps result-set name to result set. The default set produced by a `select` clause is
// named "#select".
type Decoded map[string]ResultSet

// Parse reads the JSON produced by `codeql bqrs decode --format=json`.
func Parse(data []byte) (Decoded, error) {
	var decoded Decoded
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("parsing bqrs json: %w", err)
	}
	return decoded, nil
}

// Select returns the `#select` result set.
func (d Decoded) Select() (ResultSet, error) {
	rs, ok := d[SelectSetName]
	if !ok {
		return ResultSet{}, fmt.Errorf("bqrs has no %q result set", SelectSetName)
	}
	return rs, nil
}
