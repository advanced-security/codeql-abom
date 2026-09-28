package lockfile

// Reader names what reads a lock here: the parser's release and ABOM's interpretation of what
// that parser returns. A reading recorded under another Reader is read again, because either half
// changing can change a commit's answer.
//
// Bump the interpretation whenever what this package reports about a lock changes. The parser
// half is checked against go.mod by a test, so an upgrade cannot forget it.
const Reader = parserModule + "@" + parserVersion + " interpretation/2"

const (
	parserModule  = "github.com/github/actions-lockfile/go"
	parserVersion = "v0.0.4"
)
