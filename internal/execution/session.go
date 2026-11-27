package execution

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/regular"
)

// SessionTask is an authoring session a worker runs: the session the coordinator started, the pack
// it works in and the snapshot it reads, each bound by digest, and where the pack is unpacked.
type SessionTask struct {
	Session int64  `json:"session"`
	Request int64  `json:"request,omitempty"`
	Run     int64  `json:"run,omitempty"`
	By      string `json:"by"`
	Model   string `json:"model"`
	// Workspace is the absolute path the pack is unpacked at: in a cluster the coordinator's own, so
	// every path the session names is one landing reads.
	Workspace      string `json:"workspace"`
	PackSHA256     string `json:"packSha256"`
	SnapshotSHA256 string `json:"snapshotSha256"`
	// LimitSeconds is the longest the session may run, within the life of the token its agent holds.
	LimitSeconds int64 `json:"limitSeconds"`
	// Credits bounds what the session may spend, in AI credits; zero sets no bound.
	Credits float64 `json:"credits,omitempty"`
	// Decision is the digest of the signed decision that asked for the session.
	Decision string `json:"decision,omitempty"`
	// Databases are the digests of what a draft reads of each picked repository's database, by
	// repository.
	Databases map[string]string `json:"databases,omitempty"`
}

// SessionInputs are what a session reads beside the pack: the snapshot of the store, and for a draft
// what it reads of each picked repository's database, as DraftDatabases archives it, by repository.
type SessionInputs struct {
	Snapshot  []byte
	Databases map[string][]byte
}

// SessionRead is what a worker hands the session it runs: the snapshot, and where it unpacked each
// draft database, by repository, in a directory the agent beside it does not share.
type SessionRead struct {
	Snapshot  []byte
	Databases map[string]string
}

// draftDatabase is what a draft reads of the database at db, as its worker is handed it: the manifest
// naming where its source was, and the archive of that source.
func draftDatabase(db string, limit int64) ([]byte, error) {
	return tarGz(db, []string{filepath.Join(db, "codeql-database.yml"), filepath.Join(db, "src.zip")}, limit)
}

// DraftDatabases is draftDatabase of each database at where, by repository, all of them together
// within what a coordinator hands over for one draft, naming the repository whose database is not.
func DraftDatabases(where map[string]string) (map[string][]byte, error) {
	return draftDatabasesWithin(where, databaseLimit)
}

func draftDatabasesWithin(where map[string]string, limit int64) (map[string][]byte, error) {
	out := map[string][]byte{}
	var size int64
	for repository, db := range where {
		archive, err := draftDatabase(db, limit)
		if err != nil {
			return nil, fmt.Errorf("%s's database: %w", repository, err)
		}
		if size += int64(len(archive)); size > limit {
			return nil, fmt.Errorf("%s's database takes the draft's databases past %d bytes", repository, limit)
		}
		out[repository] = archive
	}
	return out, nil
}

// What a session pod's Secret holds beside its grant, each by the variable its container reads it
// from: the Copilot token, which only the agent container is given, and the token the agent's CLI
// admits the worker's connection with, which both are.
const (
	CopilotTokenSecret    = "COPILOT_SDK_AUTH_TOKEN"
	AgentConnectionSecret = "COPILOT_CONNECTION_TOKEN"
)

// SessionFunc runs a granted session over the pack unpacked at t.Workspace, reading in. It hands report
// what the session keeps, as JSON, a run at a time as it keeps it; report answers once the report is
// written where the coordinator reads it.
type SessionFunc func(ctx context.Context, t SessionTask, in SessionRead, report func(body []byte) error) (SessionOutcome, error)

// SessionOutcome is what a session says beside the files it changed: the test directory it was bound
// to, a slash path relative to the pack, empty if never bound, beside which its test runs left what
// they leave.
type SessionOutcome struct {
	Tests string
}

// SessionManifest is what a worker says a session changed, the test directory beside which its test
// runs left what they leave, and how many reports the session made, ending with which: nothing lands
// until the coordinator has kept every one of them.
type SessionManifest struct {
	Tests   string    `json:"tests,omitempty"`
	Changed []Changed `json:"changed"`
	Reports int64     `json:"reports"`
	Head    string    `json:"head,omitempty"`
}

// Changed is a file a session left different from how it read it, by slash path relative to the
// pack: the digest of what it read, empty for one it created, and of what it left, empty for one it
// removed.
type Changed struct {
	Path   string `json:"path"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// SessionLanding is what a session left, every byte as its worker signed it.
type SessionLanding struct {
	Manifest SessionManifest
	// Files are the bytes of each file the session left changed, by path.
	Files map[string][]byte
	// Actuals and Databases are what its test runs left directly beside the bound test, by path, each
	// database an archive of its directory.
	Actuals, Databases map[string][]byte
}

// SessionReport is what a worker signs for each report a session makes while it runs: the attempt and
// grant it is for, its place in the chain, the digest of the report before it, and what the session
// kept since.
type SessionReport struct {
	Attempt  string          `json:"attempt"`
	Grant    string          `json:"grant"`
	Identity string          `json:"identity"`
	Chain    string          `json:"chain"`
	Seq      int64           `json:"seq"`
	Prev     string          `json:"prev"`
	Body     json.RawMessage `json:"body"`
}

// Reported is one report the coordinator has verified: its place in the chain, the digest it is known
// by, the digest of the report before it, and what it holds.
type Reported struct {
	Seq          int64
	Digest, Prev string
	Body         []byte
}

// SessionCalls are what a coordinator does while a session's worker runs it.
type SessionCalls struct {
	// Began is told the attempt and its grant's digest once the grant is signed and before the worker
	// is launched, so a coordinator that restarts can read what the worker reported.
	Began func(attempt, grant string) error
	// Keep keeps each report in order as it is verified; an error stops the worker and lands nothing.
	Keep func(Reported) error
	// Land lands what the session left, once its result verifies and every report it made is kept.
	Land func(SessionLanding) error
	// Stop, once closed, asks the worker to stop the session and report what it did.
	Stop <-chan struct{}
}

// The kinds of output only a session leaves.
const (
	KindManifest = "session-manifest"
	KindFile     = "file"
)

const manifestName = "session/manifest.json"

// finishing is how long a worker has, once its session ends, to upload what the session left and sign
// its result: inside the grace period its pod is given when it is stopped.
const finishing = 45 * time.Second

// maxChanged bounds the files one session may leave changed, each uploaded as its own output: a
// session changes a query, its help and its tests.
const maxChanged = 1000

// maxReport bounds one report of a session: what it kept in the seconds since the one before, of
// events clipped and printouts bounded far below it.
const maxReport = 16 << 20

func snapshotKey(prefix, attempt string) string { return prefix + attempt + "/in/snapshot.json" }

// databaseKey is where a draft's database is handed over, by the digest of its archive.
func databaseKey(prefix, attempt, sum string) string {
	return prefix + attempt + "/in/databases/" + sum + ".tgz"
}

// reportKey is where a session's report seq is written: each report once, under its own name.
func reportKey(prefix, attempt string, seq int64) string {
	return fmt.Sprintf("%s%s/reports/%08d.json", prefix, attempt, seq)
}

// RunSession runs one authoring session in a worker of this pool on the pack under root, reading in.
// It keeps each report the worker makes as the worker makes it, asks the worker to stop when
// calls.Stop closes, and hands calls.Land what the session left once the result verifies and every
// report it names is kept. A refused report stops the worker and lands nothing. A receipt is signed
// for every result read, and for a refused report.
func (r *Runner) RunSession(ctx context.Context, root string, t SessionTask, in SessionInputs,
	secrets map[string]string, calls SessionCalls) error {
	if t.Workspace == "" {
		t.Workspace = root
	}
	attempt, err := newAttempt()
	if err != nil {
		return err
	}
	if t.PackSHA256, err = r.handOver(ctx, root); err != nil {
		return err
	}
	g, key, err := r.newGrant(attempt)
	if err != nil {
		return err
	}
	secrets = maps.Clone(secrets)
	if secrets == nil {
		secrets = map[string]string{}
	}
	secrets[AttemptKeySecret] = key
	if err := r.Objects.Put(ctx, snapshotKey(g.Prefix, attempt), in.Snapshot); err != nil {
		return fmt.Errorf("handing over session %d's snapshot: %w", t.Session, err)
	}
	t.SnapshotSHA256 = digest(in.Snapshot)
	for repository, archive := range in.Databases {
		sum := digest(archive)
		if err := r.Objects.Put(ctx, databaseKey(g.Prefix, attempt, sum), archive); err != nil {
			return fmt.Errorf("handing over %s's database for session %d: %w", repository, t.Session, err)
		}
		if t.Databases == nil {
			t.Databases = map[string]string{}
		}
		t.Databases[repository] = sum
	}
	g.Session = &t
	what := fmt.Sprintf("session %d", t.Session)
	env, grantDigest, err := r.grant(ctx, g, what)
	if err != nil {
		return err
	}
	if calls.Began != nil {
		if err := calls.Began(attempt, grantDigest); err != nil {
			return err
		}
	}
	waitCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	defer r.Launch.Cleanup(context.WithoutCancel(ctx), attempt)
	if err := r.Launch.Start(waitCtx, Launch{Attempt: attempt, Grant: env, Secrets: secrets, Sealed: t.Run != 0}); err != nil {
		return fmt.Errorf("starting the worker for %s: %w", what, err)
	}
	kept := &cursor{}
	refused, waited := r.follow(waitCtx, attempt, grantDigest, kept, calls)
	if refused != nil {
		receipt := Receipt{Attempt: attempt, Grant: grantDigest, Session: t.Session, Verdict: ReceiptRefused,
			Reason: refused.Error()}
		return errors.Join(fmt.Errorf("%s: %w", what, refused), r.sign(ctx, receipt))
	}
	if waited != nil && !errors.Is(waited, ErrStopped) {
		// A worker that failed after signing its result still left it; one that did not left nothing.
		if _, err := r.Objects.Get(ctx, attestationKey(r.prefix(), attempt), maxSigned); err != nil {
			return fmt.Errorf("the worker for %s: %w", what, waited)
		}
	}
	wrap := func(err error) error { return fmt.Errorf("the result of %s: %w", what, err) }
	return r.settle(ctx, Receipt{Attempt: attempt, Grant: grantDigest, Session: t.Session},
		func(accepted Result) error {
			landing, err := r.gather(ctx, attempt, accepted)
			if err != nil {
				return err
			}
			switch m := landing.Manifest; {
			case m.Reports != kept.seq:
				return fmt.Errorf("the worker says the session made %d reports, and %d are kept", m.Reports, kept.seq)
			case m.Head != kept.head:
				return errors.New("the reports kept do not end with the one the worker says the session made last")
			}
			return calls.Land(landing)
		}, wrap, wrap)
}

// cursor is how far a session's reports are kept: the last one's place and digest, and how many bytes
// of reports were read to get there.
type cursor struct {
	seq   int64
	head  string
	bytes int64
}

func (r *Runner) reportBudget() int64 {
	if r.ReportBudget > 0 {
		return r.ReportBudget
	}
	return 256 << 20
}

func (r *Runner) reportPoll() time.Duration {
	if r.ReportPoll > 0 {
		return r.ReportPoll
	}
	return 2 * time.Second
}

// follow keeps what a session's worker reports while it runs, asks it to stop when calls.Stop closes
// or a report is refused, and answers once the worker has finished: the refusal, if any, and how the
// worker ended.
func (r *Runner) follow(ctx context.Context, attempt, grant string, c *cursor, calls SessionCalls) (refused, waited error) {
	done := make(chan error, 1)
	go func() { done <- r.Launch.Wait(ctx, attempt) }()
	tick := time.NewTicker(r.reportPoll())
	defer tick.Stop()
	keep := calls.Keep
	if keep == nil {
		keep = func(Reported) error { return nil }
	}
	stop, stopping := calls.Stop, false
	halt := func() {
		if stopping {
			return
		}
		stopping = true
		if err := r.Launch.Stop(context.WithoutCancel(ctx), attempt); err != nil {
			slog.Warn("authoring: asking a session's worker to stop", "attempt", attempt, "error", err)
		}
	}
	read := func() {
		if refused != nil {
			return
		}
		var unread *unreadReport
		switch err := r.collect(ctx, attempt, grant, c, keep); {
		case errors.As(err, &unread):
			slog.Warn("authoring: a session's report could not be read; reading it again", "attempt", attempt,
				"error", err)
		case err != nil:
			refused = err
			halt()
		}
	}
	for {
		select {
		case waited = <-done:
			// Everything the worker reported was written before it finished.
			read()
			return refused, waited
		case <-stop:
			stop = nil
			halt()
		case <-tick.C:
			read()
		}
	}
}

// unreadReport is a report that could not be read, as against one that does not verify: it is read
// again rather than refused.
type unreadReport struct{ err error }

func (e *unreadReport) Error() string { return "reading a report: " + e.err.Error() }
func (e *unreadReport) Unwrap() error { return e.err }

// collect keeps each report the worker has written since the last kept, in order, each verified as
// signed by the pool's identity and the attempt's own key, for this attempt's grant, at its place in
// the chain.
func (r *Runner) collect(ctx context.Context, attempt, grant string, c *cursor, keep func(Reported) error) error {
	read, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	var own *ecdsa.PublicKey
	for {
		signed, err := r.Objects.Get(read, reportKey(r.prefix(), attempt, c.seq+1), maxReport)
		switch {
		case errors.Is(err, ErrNotFound):
			return nil
		case errors.Is(err, ErrTooLarge):
			return fmt.Errorf("report %d: %w", c.seq+1, err)
		case err != nil:
			return &unreadReport{err}
		}
		if c.bytes += int64(len(signed)); c.bytes > r.reportBudget() {
			return fmt.Errorf("report %d: the session's reports come to more than its budget of %d bytes",
				c.seq+1, r.reportBudget())
		}
		if own == nil {
			if own, err = r.attemptKey(read, attempt, grant); err != nil {
				return fmt.Errorf("report %d: %w", c.seq+1, err)
			}
		}
		rep, err := r.verifyReport(attempt, grant, own, *c, signed)
		if err != nil {
			return fmt.Errorf("report %d: %w", c.seq+1, err)
		}
		if err := keep(rep); err != nil {
			return fmt.Errorf("keeping report %d: %w", rep.Seq, err)
		}
		c.seq, c.head = rep.Seq, rep.Digest
	}
}

// Reports keeps what a session's worker reported for attempt after report after, whose digest is
// head, as a coordinator that restarted reads what the one before it had not kept: each verified as a
// report followed while its worker ran would be.
func (r *Runner) Reports(ctx context.Context, attempt, grant string, after int64, head string,
	keep func(Reported) error) error {
	return r.collect(ctx, attempt, grant, &cursor{seq: after, head: head}, keep)
}

func (r *Runner) verifyReport(attempt, grant string, own *ecdsa.PublicKey, c cursor, signed []byte) (Reported, error) {
	predicate, id, err := r.open(signed, authz.PredicateSessionReport, own)
	if err != nil {
		return Reported{}, err
	}
	var rep SessionReport
	if err := json.Unmarshal(predicate, &rep); err != nil {
		return Reported{}, err
	}
	switch {
	case rep.Attempt != attempt || rep.Grant != grant || rep.Identity != id:
		return Reported{}, errors.New("it is for another attempt, grant or identity")
	case rep.Seq != c.seq+1:
		return Reported{}, fmt.Errorf("it says it is report %d", rep.Seq)
	case rep.Prev != c.head:
		return Reported{}, errors.New("it does not follow the report kept before it")
	}
	return Reported{Seq: rep.Seq, Digest: digest(signed), Prev: rep.Prev, Body: rep.Body}, nil
}

// gather reads everything a session's result lists, and accepts it only as a whole that agrees with
// itself: one manifest, a file for each change that left one and for no other, and what a run leaves
// only directly beside the bound test. Nothing is read before its name and kind are.
func (r *Runner) gather(ctx context.Context, attempt string, result Result) (SessionLanding, error) {
	l := SessionLanding{Files: map[string][]byte{}, Actuals: map[string][]byte{}, Databases: map[string][]byte{}}
	// The files land in the pack, which a worker refuses past packLimit.
	var files int64
	for _, o := range result.Outputs {
		if o.Kind == KindFile {
			files += o.Size
		}
	}
	if files > packLimit {
		return l, fmt.Errorf("the files the session left come to more than the pack's %d MB", packLimit>>20)
	}
	manifest := false
	for _, o := range result.Outputs {
		var slot func(body []byte) error
		switch {
		case o.Kind == KindManifest && o.Name == manifestName && !manifest:
			manifest = true
			slot = func(body []byte) error { return json.Unmarshal(body, &l.Manifest) }
		case o.Kind == KindFile && strings.HasPrefix(o.Name, "files/"):
			slot = func(body []byte) error { l.Files[strings.TrimPrefix(o.Name, "files/")] = body; return nil }
		case o.Kind == KindActual && strings.HasPrefix(o.Name, "actual/"):
			slot = func(body []byte) error { l.Actuals[strings.TrimPrefix(o.Name, "actual/")] = body; return nil }
		case o.Kind == KindTestDatabase && strings.HasPrefix(o.Name, "database/") && strings.HasSuffix(o.Name, ".tgz"):
			slot = func(body []byte) error {
				l.Databases[strings.TrimSuffix(strings.TrimPrefix(o.Name, "database/"), ".tgz")] = body
				return nil
			}
		default:
			return l, fmt.Errorf("output %s is a %s, which a session does not leave", o.Name, o.Kind)
		}
		body, err := r.fetch(ctx, attempt, o)
		if err != nil {
			return l, err
		}
		if err := slot(body); err != nil {
			return l, fmt.Errorf("output %s: %w", o.Name, err)
		}
	}
	if !manifest {
		return l, errors.New("the result names no manifest of what the session changed")
	}
	return l, l.agrees()
}

// agrees checks that what a landing holds is what its manifest says, path by path.
func (l SessionLanding) agrees() error {
	m := l.Manifest
	if m.Tests != "" && !plain(m.Tests) {
		return fmt.Errorf("%q is not a plain relative name", m.Tests)
	}
	left := map[string]string{}
	for _, c := range m.Changed {
		if _, twice := left[c.Path]; twice || !plain(c.Path) || c.Before == c.After {
			return fmt.Errorf("the manifest's change to %q is not one change to a plain relative name", c.Path)
		}
		left[c.Path] = c.After
		if body, ok := l.Files[c.Path]; c.After != "" && (!ok || digest(body) != c.After) {
			return fmt.Errorf("%s is not what the manifest says the session left there", c.Path)
		}
	}
	for p := range l.Files {
		if left[p] == "" {
			return fmt.Errorf("%s came back, and the manifest names no file the session left there", p)
		}
	}
	beside := func(rel, suffix string) bool {
		name := path.Base(rel)
		return m.Tests != "" && path.Dir(rel) == m.Tests && strings.HasSuffix(name, suffix) && name != suffix
	}
	for p := range l.Actuals {
		if !beside(p, ".actual") {
			return fmt.Errorf("%s is not what a run of %s leaves", p, m.Tests)
		}
	}
	for p := range l.Databases {
		if !beside(p, ".testproj") {
			return fmt.Errorf("%s is not what a run of %s leaves", p, m.Tests)
		}
	}
	return nil
}

// reporter hands a session's reports to its attestor, which signs each as the one after the last, and
// remembers where the chain has reached.
type reporter struct {
	a Attestor

	mu   sync.Mutex
	seq  int64
	head string
}

// send has body signed as the session's next report and written where the coordinator reads it,
// answering once it is written: a decision is reported this way before the call it decides runs.
func (p *reporter) send(ctx context.Context, body []byte) error {
	seq, head, err := p.a.Report(ctx, body)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq, p.head = seq, head
	return nil
}

// last is how many reports were written, and the digest of the last.
func (p *reporter) last() (int64, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seq, p.head
}

// runSession runs a session grant on the snapshot and pack it binds, unpacked where it says, reporting
// what the session keeps as it keeps it, then uploads each file it changed and what its test runs left,
// whether it finished or was stopped.
//
// The agent beside the worker shares the pack and can still write once the session has ended, so
// nothing the session left is read through a link, from anything but a regular file, or beyond the
// pack's bound, and what is uploaded is exactly what the manifest names.
func (w *Worker) runSession(ctx context.Context, a Attestor, g Grant, _ string, r *Result) error {
	refuse := func(why string) error {
		r.Failure = &Failure{Kind: FailedRetryable, Why: why}
		return nil
	}
	if w.Session == nil {
		return errors.New("worker: this worker runs no authoring sessions")
	}
	t := *g.Session
	if !filepath.IsAbs(t.Workspace) {
		return refuse(fmt.Sprintf("the workspace %q is not an absolute path", t.Workspace))
	}
	snapshot, err := a.Input(ctx, Input{Kind: InputSnapshot, SHA256: t.SnapshotSHA256})
	if err != nil {
		return refuse("reading the snapshot: " + err.Error())
	}
	defer os.RemoveAll(filepath.Join(w.private(), g.Attempt))
	if why := w.share(ctx, a, g, t); why != "" {
		return refuse(why)
	}
	databases, why := w.draftDatabases(ctx, a, g, t)
	if why != "" {
		return refuse(why)
	}
	pack, err := os.OpenRoot(t.Workspace)
	if err != nil {
		return err
	}
	defer func() { _ = pack.Close() }()
	// Read once the pack is prepared, so preparing it is not taken for the session's work.
	before, err := packDigests(pack)
	if err != nil {
		return err
	}
	reports := &reporter{a: a}
	outcome, err := w.Session(ctx, t, SessionRead{Snapshot: snapshot, Databases: databases},
		func(body []byte) error { return reports.send(ctx, body) })
	if err != nil {
		return refuse(err.Error())
	}
	// Stopped or not, what the session left is uploaded, inside the time its pod gives it to.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishing)
	defer cancel()
	after, err := packDigests(pack)
	if err != nil {
		return refuse("reading what the session left: " + err.Error())
	}
	m := SessionManifest{Tests: outcome.Tests, Changed: changes(before, after)}
	m.Reports, m.Head = reports.last()
	if len(m.Changed) > maxChanged {
		return refuse(fmt.Sprintf("the session left %d files changed, more than %d", len(m.Changed), maxChanged))
	}
	files, err := readLeft(pack, m.Changed, packLimit)
	if err != nil {
		return refuse("reading what the session left: " + err.Error())
	}
	for _, c := range m.Changed {
		if c.After == "" {
			continue
		}
		if err := a.Output(ctx, Output{Name: "files/" + c.Path, Kind: KindFile}, files[c.Path]); err != nil {
			return err
		}
	}
	if m.Tests != "" && plain(m.Tests) {
		if err := putLeftIn(ctx, a, pack, m.Tests); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return refuse("reading what the session's tests left: " + err.Error())
		}
	}
	manifest, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return a.Output(ctx, Output{Name: manifestName, Kind: KindManifest}, manifest)
}

// share prepares the pack a session's grant binds where the agent beside the worker cannot write, and
// only then unpacks what preparing it made at t.Workspace, which the agent shares, answering why not.
// Preparing a pack runs the CLI over the pack's own files, and the CLI quotes a file it cannot read as
// the file it should be: a link the agent planted to the worker's key would be quoted whole.
func (w *Worker) share(ctx context.Context, a Attestor, g Grant, t SessionTask) string {
	private := filepath.Join(w.private(), g.Attempt, "pack")
	if why := w.unpackAt(ctx, a, t.PackSHA256, private); why != "" {
		return why
	}
	prepared, err := packBundle(private)
	if err != nil {
		return "reading the prepared pack: " + err.Error()
	}
	if err := untarGz(prepared, t.Workspace, packLimit); err != nil {
		return "unpacking the pack: " + err.Error()
	}
	return ""
}

// draftDatabases reads each database a draft's grant binds by digest and unpacks it where the agent does
// not read, answering where each is by repository, or why not.
func (w *Worker) draftDatabases(ctx context.Context, a Attestor, g Grant, t SessionTask) (map[string]string, string) {
	if len(t.Databases) == 0 {
		return nil, ""
	}
	out := map[string]string{}
	for repository, sum := range t.Databases {
		if !plain(sum) {
			return nil, fmt.Sprintf("the database granted for %s is not named by a digest", repository)
		}
		body, err := a.Input(ctx, Input{Kind: InputDatabase, SHA256: sum})
		if err != nil {
			return nil, fmt.Sprintf("reading %s's database: %v", repository, err)
		}
		dir := filepath.Join(w.private(), g.Attempt, "databases", sum)
		if _, err := os.Stat(dir); err != nil {
			if err := untarGz(body, dir, databaseLimit); err != nil {
				return nil, fmt.Sprintf("unpacking %s's database: %v", repository, err)
			}
		}
		out[repository] = dir
	}
	return out, ""
}

// packDigests is the digest of each file of the pack a session can have written, by slash path: what
// a run leaves, caches, links and anything but a regular file are not, and a pack holding more than
// its bound is refused.
func packDigests(pack *os.Root) (map[string]string, error) {
	out := map[string]string{}
	var size int64
	err := fs.WalkDir(pack.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case p != "." && leaving(d) && d.IsDir():
			return fs.SkipDir
		case !d.Type().IsRegular() || leaving(d):
			return nil
		}
		f, err := openRegular(pack, p)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(f, packLimit-size+1))
		if err != nil {
			return err
		}
		if size += n; size > packLimit {
			return fmt.Errorf("the pack holds more than %d MB", packLimit>>20)
		}
		out[p] = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	return out, err
}

// openRegular opens a regular file at a slash path within pack, and never a pipe or a device, which
// would hold the read open, nor a name that is itself a link.
func openRegular(pack *os.Root, name string) (*os.File, error) {
	return regular.OpenIn(pack, filepath.FromSlash(name))
}

// readLeft reads each file changed names as left, from pack as openRegular opens it, each exactly what
// its digest says the session left, and all of them within limit.
func readLeft(pack *os.Root, changed []Changed, limit int64) (map[string][]byte, error) {
	out := map[string][]byte{}
	var size int64
	for _, c := range changed {
		if c.After == "" {
			continue
		}
		body, err := readIn(pack, c.Path, limit-size)
		if err != nil {
			return nil, err
		}
		if digest(body) != c.After {
			return nil, fmt.Errorf("%s changed after the session ended, so what it left cannot be told", c.Path)
		}
		size += int64(len(body))
		out[c.Path] = body
	}
	return out, nil
}

// readIn reads a regular file at a slash path within pack, as openRegular opens it, refusing one
// longer than limit.
func readIn(pack *os.Root, name string, limit int64) ([]byte, error) {
	f, err := openRegular(pack, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s is longer than this may hold", name)
	}
	return body, nil
}

// changes are the files whose digest differs between before and after, in path order.
func changes(before, after map[string]string) []Changed {
	paths := make([]string, 0, len(before)+len(after))
	for p := range before {
		paths = append(paths, p)
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	out := []Changed{}
	for _, p := range paths {
		if before[p] != after[p] {
			out = append(out, Changed{Path: p, Before: before[p], After: after[p]})
		}
	}
	return out
}

// LandBeside writes what a session's test runs left beside the test directory tests, a slash path
// under root: each .actual, and each database whole. The directory is the one the coordinator bound
// the session to, not the one its worker named.
func LandBeside(root, tests string, actuals, databases map[string][]byte) error {
	beside := func(rel, suffix string) error {
		if name := path.Base(rel); !plain(rel) || path.Dir(rel) != tests || !strings.HasSuffix(name, suffix) ||
			name == suffix {
			return fmt.Errorf("%s is not what a run of %s leaves", rel, tests)
		}
		return nil
	}
	for rel := range actuals {
		if err := beside(rel, ".actual"); err != nil {
			return err
		}
	}
	for rel := range databases {
		if err := beside(rel, ".testproj"); err != nil {
			return err
		}
	}
	for rel, archive := range databases {
		if err := replaceDatabase(filepath.Join(root, filepath.FromSlash(rel)), archive); err != nil {
			return err
		}
	}
	for rel, body := range actuals {
		file := filepath.Join(root, filepath.FromSlash(rel))
		if info, err := os.Lstat(file); err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a file", rel)
		}
		if err := os.WriteFile(file, body, 0o644); err != nil {
			return err
		}
	}
	return nil
}
