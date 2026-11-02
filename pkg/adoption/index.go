// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package adoption

// One index version, as chant's index store keeps it (chant
// packages/core/src/lifecycle/index-document.ts, layout ruled on
// INTENTIUS/behold#1025): an `index: "v1"` envelope with a stamp, the
// coverage and a GraphIR. This reader takes only what adoption needs (the
// stamp and the nodes) and checks the stamp the way chant's
// validateIndexDocument does, plus the digest: the scope key is the scope's,
// the source kind is the scope's kind, the version name is the observed time
// and the digest's first 12 hex, and the digest is sha256 over the canonical
// coverage and graph.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// IndexDocumentVersion is the only index envelope version this reader takes.
const IndexDocumentVersion = "v1"

// IndexVersion is one parsed and checked index version.
type IndexVersion struct {
	Stamp    Stamp
	Coverage []CoverageEntry
	Nodes    []Node
	// byID indexes Nodes by Node.ID.
	byID map[string]int
}

// Stamp is the index version's stamp.
type Stamp struct {
	Source      Source          `json:"source"`
	Scope       json.RawMessage `json:"scope"`
	ScopeKey    string          `json:"scopeKey"`
	ObservedAt  string          `json:"observedAt"`
	CompletedAt string          `json:"completedAt,omitempty"`
	Commit      string          `json:"commit,omitempty"`
	// Digest is sha256 hex of the canonical coverage and graph.
	Digest string `json:"digest"`
}

// Source says who produced the index.
type Source struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// CoverageEntry is what a read looked at, per type and location.
type CoverageEntry struct {
	ListedType string `json:"listedType"`
	Location   string `json:"location"`
	Verdict    string `json:"verdict"`
	Detail     string `json:"detail,omitempty"`
}

// Node is the part of a GraphIR node adoption reads. Indexed nodes carry
// ListedType (the inventory API's type name), Location, NativeID (Cloud
// Control's identifier, the ARM id, the asset name, or namespace/name) and
// Tags.
type Node struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Lexicon    string            `json:"lexicon"`
	PhysicalID string            `json:"physicalId,omitempty"`
	ListedType string            `json:"listedType,omitempty"`
	Location   string            `json:"location,omitempty"`
	NativeID   string            `json:"nativeId,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Attrs      map[string]any    `json:"attrs,omitempty"`
	Facts      json.RawMessage   `json:"facts,omitempty"`
}

// Node is the node with id, if the version holds it.
func (v *IndexVersion) Node(id string) (*Node, bool) {
	i, ok := v.byID[id]
	if !ok {
		return nil, false
	}
	return &v.Nodes[i], true
}

// Name is the version's file name, <observedAt>-<digest12>.json.
func (v *IndexVersion) Name() (string, error) {
	return VersionName(v.Stamp.ObservedAt, v.Stamp.Digest)
}

var versionNameRE = regexp.MustCompile(`^\d{8}T\d{6}Z-[0-9a-f]{12}\.json$`)
var digestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// IsVersionName reports whether name is an index version's file name.
func IsVersionName(name string) bool { return versionNameRE.MatchString(name) }

// normalizeDigest is a digest without its "sha256:" prefix, checked to be
// sha256 hex.
func normalizeDigest(d string) (string, error) {
	h := strings.TrimPrefix(d, "sha256:")
	if !digestRE.MatchString(h) {
		return "", fmt.Errorf("digest %q is not sha256 hex", d)
	}
	return h, nil
}

// VersionName is chant's indexVersionName: the observed time as
// yyyymmddThhmmssZ, a dash, the digest's first 12 hex, ".json".
func VersionName(observedAt, digest string) (string, error) {
	h, err := normalizeDigest(digest)
	if err != nil {
		return "", err
	}
	t, err := time.Parse(time.RFC3339Nano, observedAt)
	if err != nil {
		return "", fmt.Errorf("observedAt %q is not a time: %w", observedAt, err)
	}
	return t.UTC().Format("20060102T150405Z") + "-" + h[:12] + ".json", nil
}

// ParseIndexVersion parses one index version and checks its stamp and digest.
func ParseIndexVersion(data []byte) (*IndexVersion, error) {
	raw, err := decodeJSON(data)
	if err != nil {
		return nil, fmt.Errorf("index version: %w", err)
	}
	doc, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("index version: not a JSON object")
	}
	if v, _ := doc["index"].(string); v != IndexDocumentVersion {
		return nil, fmt.Errorf("index document version %v is not %s", doc["index"], IndexDocumentVersion)
	}
	var env struct {
		Stamp    Stamp           `json:"stamp"`
		Coverage []CoverageEntry `json:"coverage"`
		Graph    struct {
			Nodes []Node `json:"nodes"`
		} `json:"graph"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("index version: %w", err)
	}
	if _, ok := doc["graph"]; !ok {
		return nil, fmt.Errorf("index version has no graph")
	}
	v := &IndexVersion{Stamp: env.Stamp, Coverage: env.Coverage, Nodes: env.Graph.Nodes,
		byID: make(map[string]int, len(env.Graph.Nodes))}
	for i, n := range v.Nodes {
		if _, dup := v.byID[n.ID]; dup {
			return nil, fmt.Errorf("index version holds node %q twice", n.ID)
		}
		v.byID[n.ID] = i
	}

	// The stamp agrees with itself (chant's validateIndexDocument).
	scope, err := decodeJSON(env.Stamp.Scope)
	if err != nil {
		return nil, fmt.Errorf("index stamp scope: %w", err)
	}
	scopeMap, ok := scope.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("index stamp scope is not a JSON object")
	}
	if kind, _ := scopeMap["kind"].(string); env.Stamp.Source.Kind != kind {
		return nil, fmt.Errorf("index stamp source kind %q does not match its scope kind %q", env.Stamp.Source.Kind, kind)
	}
	key, err := ScopeKey(scopeMap)
	if err != nil {
		return nil, err
	}
	if env.Stamp.ScopeKey != key {
		return nil, fmt.Errorf("index stamp scopeKey %q is not its scope's key %q", env.Stamp.ScopeKey, key)
	}
	if _, err := v.Name(); err != nil {
		return nil, fmt.Errorf("index stamp: %w", err)
	}

	// The digest is the document's.
	body := map[string]any{}
	for _, k := range []string{"coverage", "graph"} {
		if x, ok := doc[k]; ok {
			body[k] = x
		}
	}
	canon, err := canonicalJSON(body)
	if err != nil {
		return nil, fmt.Errorf("index version: %w", err)
	}
	sum := sha256.Sum256([]byte(canon))
	got := hex.EncodeToString(sum[:])
	stamped, err := normalizeDigest(env.Stamp.Digest)
	if err != nil {
		return nil, fmt.Errorf("index stamp: %w", err)
	}
	if got != stamped {
		return nil, fmt.Errorf("index version's coverage and graph hash to %s, not the stamped digest %s", got, stamped)
	}
	return v, nil
}

// ReadIndexVersion reads and checks the index version at path. The file's
// name must be the version's own name.
func ReadIndexVersion(path string) (*IndexVersion, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	v, err := ParseIndexVersion(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	name, _ := v.Name()
	if base := filepath.Base(path); base != name {
		return nil, fmt.Errorf("%s: the file is named %s, but its stamp names version %s", path, base, name)
	}
	return v, nil
}

// VersionPath is where a filesystem index store under storeDir keeps the
// version an index reference names: <storeDir>/index/<scopeKey>/<version>.
func VersionPath(storeDir string, ref IndexRef) string {
	return filepath.Join(storeDir, "index", filepath.FromSlash(ref.ScopeKey), ref.Version)
}

// ScopeKey is chant's indexScopeKey for a decoded scope:
// <source path>/<identity segments>/<scope digest>.
func ScopeKey(scope map[string]any) (string, error) {
	kind, _ := scope["kind"].(string)
	canon, segments, source, err := canonicalScope(kind, scope)
	if err != nil {
		return "", err
	}
	text, err := canonicalJSON(canon)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(text))
	parts := []string{source}
	for _, s := range segments {
		enc, err := encodeSegment(s)
		if err != nil {
			return "", err
		}
		parts = append(parts, enc)
	}
	parts = append(parts, hex.EncodeToString(sum[:])[:12])
	return strings.Join(parts, "/"), nil
}

// canonicalScope is chant's canonicalScope, with the scope path's identity
// segments and source path.
func canonicalScope(kind string, s map[string]any) (canon map[string]any, segments []string, source string, err error) {
	str := func(k string) string { v, _ := s[k].(string); return v }
	has := func(k string) bool { _, ok := s[k]; return ok }
	switch kind {
	case "cloud":
		substrate, err := encodeSegment(str("substrate"))
		if err != nil {
			return nil, nil, "", fmt.Errorf("index scope substrate: %w", err)
		}
		canon = map[string]any{"kind": "cloud", "substrate": s["substrate"], "scopeId": s["scopeId"],
			"locations": starOrSorted(s["locations"]), "types": starOrSorted(s["types"])}
		if tf, ok := s["tagFilter"].(map[string]any); ok && len(tf) > 0 {
			canon["tagFilter"] = tf
		}
		return canon, []string{str("scopeId")}, "cloud/" + substrate, nil
	case "engine":
		engine, err := encodeSegment(str("engine"))
		if err != nil {
			return nil, nil, "", fmt.Errorf("index scope engine: %w", err)
		}
		canon = map[string]any{"kind": "engine", "engine": s["engine"]}
		for _, k := range []string{"estate", "project", "stack", "backend"} {
			if has(k) {
				canon[k] = s[k]
			}
		}
		if has("estate") {
			segments = []string{str("estate")}
		} else {
			project, stack := "-", "-"
			if v, ok := s["project"].(string); ok {
				project = v
			}
			if v, ok := s["stack"].(string); ok {
				stack = v
			}
			segments = []string{project, stack}
		}
		return canon, segments, "engine/" + engine, nil
	case "chant":
		canon = map[string]any{"kind": "chant", "member": s["member"], "env": s["env"], "lexicon": s["lexicon"]}
		stack := "-"
		if has("stack") {
			canon["stack"] = s["stack"]
			if v, ok := s["stack"].(string); ok {
				stack = v
			}
		}
		return canon, []string{str("member"), str("env"), stack, str("lexicon")}, "chant", nil
	}
	return nil, nil, "", fmt.Errorf("index scope kind %q is not cloud, engine or chant", kind)
}

// starOrSorted is "*" as it is, or a list of strings sorted and deduplicated
// (chant's sortUnique: JavaScript's default sort).
func starOrSorted(v any) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range list {
		s, _ := e.(string)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sortUTF16(out)
	res := make([]any, len(out))
	for i, s := range out {
		res[i] = s
	}
	return res
}

// encodeSegment is chant's encodeIndexSegment: encodeURIComponent with !'()*
// encoded too, and "." and ".." spelled out.
func encodeSegment(seg string) (string, error) {
	if seg == "" {
		return "", fmt.Errorf("an index key segment is never empty")
	}
	const upper = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upper[c>>4])
		b.WriteByte(upper[c&0xf])
	}
	switch enc := b.String(); enc {
	case ".":
		return "%2E", nil
	case "..":
		return "%2E%2E", nil
	default:
		return enc, nil
	}
}
