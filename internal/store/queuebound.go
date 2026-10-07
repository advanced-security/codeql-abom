package store

import "encoding/json"

// QueueBound keeps a queue read to the repositories the crawl claims inside, which is the owned
// closure. The zero value admits every row.
type QueueBound struct {
	names   []string
	bounded bool
}

// BoundTo admits the repositories named, as owner/repo in any case. An empty list admits none.
func BoundTo(names []string) QueueBound {
	return QueueBound{names: append([]string{}, names...), bounded: true}
}

// Admits is the condition a frontier row meets inside the bound, and its argument. qualifier
// prefixes the owner and repo columns, as "abom_frontier." does where a join makes them ambiguous.
func (b QueueBound) Admits(qualifier string) (string, []any) {
	if !b.bounded {
		return "1", nil
	}
	// A list of strings always encodes.
	list, _ := json.Marshal(b.names)
	return `LOWER(COALESCE(` + qualifier + `owner, '') || '/' || COALESCE(` + qualifier +
		`repo, '')) IN (SELECT LOWER(value) FROM json_each(?))`, []any{string(list)}
}
