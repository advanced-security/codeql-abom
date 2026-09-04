package sarifin

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Provenance identifies the source a SARIF run analysed.
//
// It is more reliable than the filename: MRVA and sarif-sql both flatten owner/name into the
// filename with a separator that repository names may themselves contain, whereas the run
// records the repository URI and revision verbatim.
type Provenance struct {
	Repository string
	CommitSHA  string
}

// ReadProvenance scans a SARIF document for versionControlProvenance.
//
// The whole document is walked because provenance may follow the results array, but nothing is
// retained: results are skipped rather than decoded.
func ReadProvenance(r io.Reader) (Provenance, error) {
	dec := json.NewDecoder(r)
	if err := expectDelim(dec, '{'); err != nil {
		return Provenance{}, err
	}

	for dec.More() {
		key, err := readKey(dec)
		if err != nil {
			return Provenance{}, err
		}
		if key != "runs" {
			if err := skipValue(dec); err != nil {
				return Provenance{}, err
			}
			continue
		}
		return provenanceFromRuns(dec)
	}
	return Provenance{}, nil
}

func provenanceFromRuns(dec *json.Decoder) (Provenance, error) {
	if err := expectDelim(dec, '['); err != nil {
		return Provenance{}, err
	}

	var found Provenance
	for dec.More() {
		if err := expectDelim(dec, '{'); err != nil {
			return Provenance{}, err
		}
		for dec.More() {
			key, err := readKey(dec)
			if err != nil {
				return Provenance{}, err
			}
			if key != "versionControlProvenance" {
				if err := skipValue(dec); err != nil {
					return Provenance{}, err
				}
				continue
			}
			var entries []struct {
				RepositoryURI string `json:"repositoryUri"`
				RevisionID    string `json:"revisionId"`
			}
			if err := dec.Decode(&entries); err != nil {
				return Provenance{}, fmt.Errorf("decoding provenance: %w", err)
			}
			if found.Repository == "" && len(entries) > 0 {
				found.Repository = repositoryFromURI(entries[0].RepositoryURI)
				found.CommitSHA = entries[0].RevisionID
			}
		}
		if err := expectDelim(dec, '}'); err != nil {
			return Provenance{}, err
		}
	}
	if err := expectDelim(dec, ']'); err != nil {
		return Provenance{}, err
	}
	return found, nil
}

// repositoryFromURI reduces a repository URL to owner/name, returning "" when it is not in
// that shape.
func repositoryFromURI(uri string) string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(uri), "/")
	if trimmed == "" {
		return ""
	}
	if i := strings.Index(trimmed, "://"); i >= 0 {
		trimmed = trimmed[i+3:]
		if slash := strings.IndexByte(trimmed, '/'); slash >= 0 {
			trimmed = trimmed[slash+1:]
		} else {
			return ""
		}
	}
	trimmed = strings.TrimSuffix(trimmed, ".git")

	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}
