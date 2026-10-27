// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

// Package adoption reads an `adopt: v1` adoption file and the index version
// it names (GitHub issue #2286; ruled on #2284, Q1 and Q7). An adoption file
// says which indexed objects an engine should adopt and under which address:
//
//	{
//	  "adopt": "v1",
//	  "index": {"scopeKey": "cloud/aws/123456789012/<scope digest>",
//	            "version": "<observedAt>-<digest12>.json", "digest": "sha256:..."},
//	  "select": [{"node": "aws/123456789012/us-east-1/AWS::S3::Bucket/my-bucket",
//	              "as": "aws_s3_bucket.my_bucket", "type": "aws_s3_bucket"}]
//	}
//
// Its TypeScript type and JSON Schema live in chant core beside the index
// schema (INTENTIUS/chant#4536); the Go engines read the JSON through this
// package. `as` is the engine's address (a Terraform address for choudoufu, a
// URN name for plunge, an entity name for chant). `type` is the engine's
// type, and may be left out only when the inventory type joins to exactly one
// engine type: pkg/registry maps 19 CloudFormation types to more than one
// Terraform type, and those need it ([Selection.EngineType]).
//
// The index proposes and the live read decides: this package checks only that
// the file and the version agree (scope key, version name, digest, every
// selected node present). Each engine then reads every object again through
// its own provider before it writes a marker. Like the rest of pkg/, it
// imports nothing from this module's internal tree, so plunge can import it.
package adoption

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// FormatVersion is the only adoption file version this package reads.
const FormatVersion = "v1"

// File is one adoption file.
type File struct {
	Adopt  string      `json:"adopt"`
	Index  IndexRef    `json:"index"`
	Select []Selection `json:"select"`
}

// IndexRef names one index version.
type IndexRef struct {
	// ScopeKey is the version's stamp.scopeKey, the store path its scope maps to.
	ScopeKey string `json:"scopeKey"`
	// Version is the version's file name, <observedAt>-<digest12>.json.
	Version string `json:"version"`
	// Digest is the version's stamp.digest, with or without "sha256:".
	Digest string `json:"digest"`
}

// Selection is one indexed object to adopt.
type Selection struct {
	// Node is the index node's id.
	Node string `json:"node"`
	// As is the engine's address for it.
	As string `json:"as"`
	// Type is the engine's type; empty when the inventory type joins to one.
	Type string `json:"type,omitempty"`
}

// Parse decodes an adoption file and checks it on its own ([File.Validate]).
// A field this version does not know is refused rather than ignored, since
// it could narrow or widen what is adopted.
func Parse(data []byte) (*File, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f File
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("adoption file: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("adoption file: trailing data after the JSON object")
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return &f, nil
}

// ReadFile reads and parses the adoption file at path.
func ReadFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// Validate checks the file on its own: the version, an index reference whose
// version name carries the digest's first 12 hex, and at least one selection,
// each naming a node and an address, with no node or address twice.
func (f *File) Validate() error {
	if f.Adopt != FormatVersion {
		return fmt.Errorf("adoption file version %q is not %s", f.Adopt, FormatVersion)
	}
	if f.Index.ScopeKey == "" {
		return fmt.Errorf("adoption file names no index scopeKey")
	}
	if strings.HasPrefix(f.Index.ScopeKey, "/") || strings.Contains("/"+f.Index.ScopeKey+"/", "/../") {
		return fmt.Errorf("adoption file's index scopeKey %q is not a store path", f.Index.ScopeKey)
	}
	if !IsVersionName(f.Index.Version) {
		return fmt.Errorf("adoption file's index version %q is not <yyyymmddThhmmssZ>-<digest12>.json", f.Index.Version)
	}
	digest, err := normalizeDigest(f.Index.Digest)
	if err != nil {
		return fmt.Errorf("adoption file's index %w", err)
	}
	if want := digest[:12] + ".json"; !strings.HasSuffix(f.Index.Version, "-"+want) {
		return fmt.Errorf("adoption file's index version %s does not end in its digest's first 12 hex (%s)",
			f.Index.Version, digest[:12])
	}
	if len(f.Select) == 0 {
		return fmt.Errorf("adoption file selects no node")
	}
	nodes, addrs := map[string]bool{}, map[string]bool{}
	for i, s := range f.Select {
		switch {
		case s.Node == "":
			return fmt.Errorf("adoption file's selection %d names no node", i)
		case s.As == "":
			return fmt.Errorf("adoption file's selection of %s names no address (as)", s.Node)
		case nodes[s.Node]:
			return fmt.Errorf("adoption file selects node %s twice", s.Node)
		case addrs[s.As]:
			return fmt.Errorf("adoption file adopts two nodes as %s", s.As)
		}
		nodes[s.Node], addrs[s.As] = true, true
	}
	return nil
}

// Adoption is an adoption file resolved against the index version it names.
type Adoption struct {
	File    *File
	Index   *IndexVersion
	Entries []Entry
}

// Entry is one selection with the index node it names.
type Entry struct {
	Selection
	Node *Node
}

// Resolve checks f against v, the index version read for it: the scope key,
// the version name and the digest are the ones f names, and v holds every
// selected node. A refusal names what disagrees.
func Resolve(f *File, v *IndexVersion) (*Adoption, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	if v.Stamp.ScopeKey != f.Index.ScopeKey {
		return nil, fmt.Errorf("the index version's scopeKey is %s, not the adoption file's %s",
			v.Stamp.ScopeKey, f.Index.ScopeKey)
	}
	want, _ := normalizeDigest(f.Index.Digest)
	got, err := normalizeDigest(v.Stamp.Digest)
	if err != nil {
		return nil, fmt.Errorf("index stamp: %w", err)
	}
	if got != want {
		return nil, fmt.Errorf("the index version's digest is %s, not the adoption file's %s", got, want)
	}
	name, err := v.Name()
	if err != nil {
		return nil, err
	}
	if name != f.Index.Version {
		return nil, fmt.Errorf("the index version is %s, not the adoption file's %s", name, f.Index.Version)
	}
	a := &Adoption{File: f, Index: v}
	var missing []string
	for _, s := range f.Select {
		n, ok := v.Node(s.Node)
		if !ok {
			missing = append(missing, s.Node)
			continue
		}
		a.Entries = append(a.Entries, Entry{Selection: s, Node: n})
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("index version %s holds no node %s", name, strings.Join(missing, ", "))
	}
	return a, nil
}

// Open reads the adoption file at path and the version it names from the
// filesystem index store under storeDir ([VersionPath]), and resolves one
// against the other.
func Open(path, storeDir string) (*Adoption, error) {
	f, err := ReadFile(path)
	if err != nil {
		return nil, err
	}
	v, err := ReadIndexVersion(VersionPath(storeDir, f.Index))
	if err != nil {
		return nil, err
	}
	return Resolve(f, v)
}

// EngineType is the engine type to adopt the selection as, given the engine
// types the node's inventory type joins to (for aws, pkg/registry's
// TFTypesForCFNType). With Type set it must be one of them, when there are
// any; without it there must be exactly one. Every refusal lists the
// candidates.
func (s Selection) EngineType(candidates []string) (string, error) {
	if s.Type != "" {
		if len(candidates) == 0 {
			return s.Type, nil
		}
		for _, c := range candidates {
			if c == s.Type {
				return s.Type, nil
			}
		}
		return "", fmt.Errorf("%s: type %s is not one the listed type joins to (%s)", s.Node, s.Type,
			strings.Join(candidates, ", "))
	}
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("%s: its listed type joins to no engine type, and the selection names none", s.Node)
	case 1:
		return candidates[0], nil
	}
	return "", fmt.Errorf("%s: its listed type joins to %s; the selection must name one as its type", s.Node,
		strings.Join(candidates, ", "))
}
