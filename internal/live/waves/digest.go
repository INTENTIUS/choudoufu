// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package waves

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// DigestPrefix starts every digest this package prints, so a reader can
// tell which hash it is and a later version can change the hash without a
// digest from one being mistaken for the other.
const DigestPrefix = "sha256:"

// digestVersion is folded into every root and set digest. It moves when
// what a digest covers moves, so an old digest never matches a new one.
const digestVersion = "choudoufu-set-digest/1"

// RootPlan is one root of a set plan document (GitHub issue #1752's -json
// output): only the fields the digest reads.
type RootPlan struct {
	Root   string          `json:"root"`
	Estate string          `json:"estate"`
	Status string          `json:"status"`
	Error  string          `json:"error"`
	Plan   json.RawMessage `json:"plan"`
}

// StatusPlanned is the status of a root whose plan was written.
const StatusPlanned = "planned"

// SetDocument is the part of a set plan document this package reads.
type SetDocument struct {
	Roots []RootPlan `json:"roots"`
}

// ParseSetDocument reads a set plan document. It requires a top-level
// "roots" array and a "root" on every element.
func ParseSetDocument(data []byte) (*SetDocument, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	if _, ok := top["roots"]; !ok {
		return nil, fmt.Errorf(`no top-level "roots": this is not a set plan document`)
	}
	var doc SetDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("reading the set plan document: %w", err)
	}
	for i, r := range doc.Roots {
		if r.Root == "" {
			return nil, fmt.Errorf(`roots[%d] has no "root"`, i)
		}
	}
	return &doc, nil
}

// planChanges is the part of a stock machine-readable plan the digest
// covers. Each element is kept raw so nothing it carries is dropped.
type planChanges struct {
	Errored         bool                       `json:"errored"`
	ResourceChanges []json.RawMessage          `json:"resource_changes"`
	OutputChanges   map[string]json.RawMessage `json:"output_changes"`
}

// changeHead is what the digest reads from a resource change to sort it
// and to tell a no-op.
type changeHead struct {
	Address string `json:"address"`
	Deposed string `json:"deposed"`
	Change  struct {
		Actions []string `json:"actions"`
	} `json:"change"`
}

type outputHead struct {
	Actions []string `json:"actions"`
}

func isNoOp(actions []string) bool {
	return len(actions) == 1 && actions[0] == "no-op"
}

// canonical decodes raw and encodes it again: object keys sorted, no
// insignificant whitespace, numbers as their literal text.
func canonical(raw json.RawMessage) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// rootCanon is what a root digest is computed over.
type rootCanon struct {
	Version         string                     `json:"v"`
	Root            string                     `json:"root"`
	Estate          string                     `json:"estate"`
	Status          string                     `json:"status"`
	Error           string                     `json:"error,omitempty"`
	HasPlan         bool                       `json:"has_plan"`
	Errored         bool                       `json:"errored"`
	ResourceChanges []json.RawMessage          `json:"resource_changes"`
	OutputChanges   map[string]json.RawMessage `json:"output_changes"`
}

// RootDigest is r's digest. See the package documentation for what it
// covers.
func RootDigest(r RootPlan) (string, error) {
	c := rootCanon{
		Version:         digestVersion,
		Root:            r.Root,
		Estate:          r.Estate,
		Status:          r.Status,
		Error:           r.Error,
		ResourceChanges: []json.RawMessage{},
		OutputChanges:   map[string]json.RawMessage{},
	}
	if len(bytes.TrimSpace(r.Plan)) > 0 && !bytes.Equal(bytes.TrimSpace(r.Plan), []byte("null")) {
		c.HasPlan = true
		var p planChanges
		if err := json.Unmarshal(r.Plan, &p); err != nil {
			return "", fmt.Errorf("root %s: reading its plan: %w", r.Root, err)
		}
		c.Errored = p.Errored

		type keyed struct {
			address, deposed string
			raw              json.RawMessage
		}
		var rcs []keyed
		for i, raw := range p.ResourceChanges {
			var h changeHead
			if err := json.Unmarshal(raw, &h); err != nil {
				return "", fmt.Errorf("root %s: resource_changes[%d]: %w", r.Root, i, err)
			}
			if isNoOp(h.Change.Actions) {
				continue
			}
			canon, err := canonical(raw)
			if err != nil {
				return "", fmt.Errorf("root %s: resource_changes[%d]: %w", r.Root, i, err)
			}
			rcs = append(rcs, keyed{h.Address, h.Deposed, canon})
		}
		sort.SliceStable(rcs, func(i, j int) bool {
			if rcs[i].address != rcs[j].address {
				return rcs[i].address < rcs[j].address
			}
			return rcs[i].deposed < rcs[j].deposed
		})
		for _, k := range rcs {
			c.ResourceChanges = append(c.ResourceChanges, k.raw)
		}

		for name, raw := range p.OutputChanges {
			var h outputHead
			if err := json.Unmarshal(raw, &h); err != nil {
				return "", fmt.Errorf("root %s: output_changes[%q]: %w", r.Root, name, err)
			}
			if isNoOp(h.Actions) {
				continue
			}
			canon, err := canonical(raw)
			if err != nil {
				return "", fmt.Errorf("root %s: output_changes[%q]: %w", r.Root, name, err)
			}
			c.OutputChanges[name] = canon
		}
	}
	// json.Marshal sorts the OutputChanges keys, and every element was
	// re-encoded by canonical, so the whole encoding is canonical.
	enc, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(enc)
	return DigestPrefix + hex.EncodeToString(sum[:]), nil
}

// RootDigestEntry is one root and its digest, the input to [SetDigest].
type RootDigestEntry struct {
	Root   string `json:"root"`
	Digest string `json:"digest"`
}

// SetDigest is the digest over a set of root digests: independent of their
// order, and different whenever any one of them is. It refuses a set that
// names one root twice, which has no single change set for that root.
func SetDigest(entries []RootDigestEntry) (string, error) {
	sorted := append([]RootDigestEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Root < sorted[j].Root })
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%d\n", digestVersion, len(sorted))
	for i, e := range sorted {
		if i > 0 && sorted[i-1].Root == e.Root {
			return "", fmt.Errorf("the set names root %s twice", e.Root)
		}
		// Length-prefixed, so no choice of root names can make two
		// different sets frame to the same bytes.
		fmt.Fprintf(h, "%d:%s%d:%s\n", len(e.Root), e.Root, len(e.Digest), e.Digest)
	}
	return DigestPrefix + hex.EncodeToString(h.Sum(nil)), nil
}

// DocumentDigests computes every root's digest in doc and the digest of
// the whole set, keyed by root.
func DocumentDigests(doc *SetDocument) (map[string]string, string, error) {
	byRoot := make(map[string]string, len(doc.Roots))
	entries := make([]RootDigestEntry, 0, len(doc.Roots))
	for _, r := range doc.Roots {
		d, err := RootDigest(r)
		if err != nil {
			return nil, "", err
		}
		if _, dup := byRoot[r.Root]; dup {
			return nil, "", fmt.Errorf("the set names root %s twice", r.Root)
		}
		byRoot[r.Root] = d
		entries = append(entries, RootDigestEntry{Root: r.Root, Digest: d})
	}
	set, err := SetDigest(entries)
	if err != nil {
		return nil, "", err
	}
	return byRoot, set, nil
}
