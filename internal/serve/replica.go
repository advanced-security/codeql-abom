package serve

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

const primaryReadCookie = "abom-primary-read"

// SetReplica creates independent read caches without copying mutexes from the primary server.
func (s *Server) SetReplica(backend store.Backend, maxLag time.Duration) {
	s.Replica = &Server{
		Store: backend, Graphviz: s.Graphviz, GroupProperty: s.GroupProperty,
		Discovery: s.Discovery, Pools: s.Pools, RecheckFloor: s.RecheckFloor, Log: s.Log,
	}
	s.MaxReplicaLag = maxLag
}

func replicaRead(path string) bool {
	for _, prefix := range []string{
		"/api/summary", "/api/actions", "/api/repositories", "/api/relationships",
		"/api/workflows", "/api/jobs", "/api/exposure", "/api/advisories", "/api/remediation",
		"/api/bom", "/api/graph", "/api/node/", "/api/views", "/api/findings",
		"/api/filters", "/api/properties", "/api/owned-scope",
	} {
		if path == prefix || strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")+"/") {
			return true
		}
	}
	return false
}

func (s *Server) routeReplica(primary, replica http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Abom-Read-Source", "primary")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.SetCookie(w, &http.Cookie{Name: primaryReadCookie, Value: "1", Path: "/",
				HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
			primary.ServeHTTP(w, r)
			return
		}
		if !replicaRead(r.URL.Path) {
			primary.ServeHTTP(w, r)
			return
		}
		current, primaryErr := s.Store.ReplicationState()
		replayed, replicaErr := s.Replica.Store.ReplicationState()
		lag, known, caughtUp := replicaLag(current, replayed)
		if primaryErr != nil || replicaErr != nil {
			known = false
			w.Header().Set("X-Abom-Replica-Error", "replication state unavailable")
			if s.Log != nil {
				fmt.Fprintf(s.Log, "serve: replica state unavailable: primary=%v replica=%v\n", primaryErr, replicaErr)
			}
		}
		if !known {
			w.Header().Set("X-Abom-Replica-Lag-Ms", "unknown")
			primary.ServeHTTP(w, r)
			return
		}
		millis := lag.Milliseconds()
		if lag%time.Millisecond != 0 {
			millis++
		}
		w.Header().Set("X-Abom-Replica-Lag-Ms", strconv.FormatInt(millis, 10))
		_, cookieErr := r.Cookie(primaryReadCookie)
		needsPrimary := cookieErr == nil
		if !caughtUp && (needsPrimary || s.MaxReplicaLag == 0) || lag > s.MaxReplicaLag {
			primary.ServeHTTP(w, r)
			return
		}
		if needsPrimary {
			http.SetCookie(w, &http.Cookie{Name: primaryReadCookie, Value: "", Path: "/", MaxAge: -1,
				HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
		}
		w.Header().Set("X-Abom-Read-Source", "replica")
		replica.ServeHTTP(w, r)
	})
}

func replicaLag(primary, replica store.ReplicaState) (time.Duration, bool, bool) {
	if primary.Identity == "" || primary.Identity != replica.Identity ||
		primary.Database != replica.Database || primary.Schema != replica.Schema {
		return 0, false, false
	}
	current, currentOK := walPosition(primary.Position)
	replayed, replayedOK := walPosition(replica.Position)
	if !replica.Recovery || !currentOK || !replayedOK {
		return 0, false, false
	}
	if replayed >= current {
		return 0, true, true
	}
	if replica.ReplayAt == nil || primary.ObservedAt.Before(*replica.ReplayAt) {
		return 0, false, false
	}
	return primary.ObservedAt.Sub(*replica.ReplayAt), true, false
}

func walPosition(raw string) (uint64, bool) {
	high, low, ok := strings.Cut(raw, "/")
	if !ok {
		return 0, false
	}
	h, e1 := strconv.ParseUint(high, 16, 32)
	l, e2 := strconv.ParseUint(low, 16, 32)
	return h<<32 | l, e1 == nil && e2 == nil
}
