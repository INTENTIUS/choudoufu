// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command azurerm-id-gen writes live/azurerm-ids.json: for every azurerm
// managed resource type at the pinned provider version, the ARM id template
// its importer parses, with each value segment mapped to the configuration
// argument that supplies it.
//
// Why (issue #1122's ruling, 2026-10-19, under epic #1120). azurerm types
// without a provider resource identity schema are identified by a schema
// rule over resource_group_name + name, "with the ARM type taken from a
// table generated from azurerm's id parsers, committed with its generator
// and pinned like the identity golden". This is that table. It is evidence
// only: nothing in internal/live reads it yet, and wiring it into admission
// is a later unit.
//
// Where the template comes from. Each resource registers either an
// Importer (untyped pluginsdk.Resource) or an IDValidationFunc (typed
// sdk.Resource), and that expression is the provider's own statement of
// which id it accepts. The generator follows it, with go/parser over the
// checkout and its vendor/ tree, to the id type it parses: a go-azure-sdk
// or go-azure-helpers resourceids type (commonids included), read from its
// Segments(), or a hand parser under internal/services/*/parse, read from
// its ID() format string. No regular expression reads Go source here.
//
// The mapping (mapSegments in mapping.go) is checked against the provider
// schema JSON: an argument the type does not declare, or declares as
// computed-only, is never mapped to. Every type gets one class - template,
// parent-id, identity-schema or unmapped - and an unmapped one says why.
//
// Usage:
//
//	go run ./tools/azurerm-id-gen -src <azurerm checkout at the pin> -schema <tofu providers schema -json output>
//
// The checkout's version/VERSION and git HEAD, and a digest of the azurerm
// half of the schema, are recorded as the artifact's pin;
// TestRegeneratingFromThePinReproducesTheArtifact proves a run against them
// reproduces the committed file.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const azurermSchemaKey = "registry.opentofu.org/hashicorp/azurerm"

// artifact is the file's whole shape.
type artifact struct {
	GeneratedBy string         `json:"generated_by"`
	Provider    string         `json:"provider"`
	Pin         pin            `json:"pin"`
	Counts      map[string]int `json:"counts"`
	Types       []entry        `json:"types"`
}

type pin struct {
	Version      string `json:"version"`
	Commit       string `json:"commit"`
	SchemaSHA256 string `json:"schema_sha256"`
}

// entry is one resource type's row.
type entry struct {
	Type  string `json:"type"`
	Class string `json:"class"`
	// Via is the expression the id was read from: Importer or IDValidationFunc.
	Via    string    `json:"via,omitempty"`
	Source *idSource `json:"source,omitempty"`
	// Template is the ARM id with value segments as {name}.
	Template   string      `json:"template,omitempty"`
	Components []component `json:"components,omitempty"`
	// TemplateClass, on an identity-schema row, is what the template alone
	// classifies as: template, parent-id or unmapped.
	TemplateClass  string   `json:"template_class,omitempty"`
	IdentitySchema []string `json:"identity_schema,omitempty"`
	Reason         string   `json:"reason,omitempty"`
	Detail         string   `json:"detail,omitempty"`
}

func main() {
	var (
		src    = flag.String("src", "", "the terraform-provider-azurerm checkout at the pinned tag")
		schema = flag.String("schema", "", "`tofu providers schema -json` output that includes hashicorp/azurerm at the same version")
		out    = flag.String("out", "live/azurerm-ids.json", "where to write the artifact")
	)
	flag.Parse()
	if *src == "" || *schema == "" {
		fmt.Fprintln(os.Stderr, "azurerm-id-gen: -src and -schema are required")
		os.Exit(2)
	}
	a, err := generate(*src, *schema)
	if err != nil {
		fmt.Fprintln(os.Stderr, "azurerm-id-gen:", err)
		os.Exit(1)
	}
	data, err := encode(a)
	if err != nil {
		fmt.Fprintln(os.Stderr, "azurerm-id-gen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "azurerm-id-gen:", err)
		os.Exit(1)
	}
	printSummary(os.Stdout, a)
}

func encode(a *artifact) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(a); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// generate is the whole pipeline, kept free of flags and output so tests
// drive it on fixtures.
func generate(src, schemaPath string) (*artifact, error) {
	schemas, digest, err := readSchema(schemaPath)
	if err != nil {
		return nil, err
	}
	version, commit := sourcePin(src)
	l, err := newLoader(src)
	if err != nil {
		return nil, err
	}
	regs, err := l.registrations()
	if err != nil {
		return nil, err
	}
	byType := map[string]registration{}
	for _, r := range regs {
		byType[r.Type] = r
	}

	// Pass one: every registered type's id source, so pass two can ask
	// which type owns a given prefix.
	sources := map[string]*idSource{}
	problems := map[string]string{}
	unresolved := map[string]*idSource{}
	for name := range schemas {
		r, ok := byType[name]
		switch {
		case !ok:
			problems[name] = "not found in the provider's registrations"
		case r.Problem != "":
			problems[name] = r.Problem
		default:
			src, problem := l.idTypeOf(r)
			if problem != "" {
				problems[name] = problem
				unresolved[name] = src
				continue
			}
			sources[name] = src
		}
	}
	index := map[string][]string{}
	for name, src := range sources {
		k := normalizedTemplate(src.Segments)
		index[k] = append(index[k], name)
	}
	for k := range index {
		sort.Strings(index[k])
	}
	parentTypes := func(prefix []segment) []string { return index[normalizedTemplate(prefix)] }
	typeTemplate := func(name string) (string, bool) {
		if src, ok := sources[name]; ok {
			return normalizedTemplate(src.Segments), true
		}
		return "", false
	}

	var entries []entry
	for name, ts := range schemas {
		src := sources[name]
		if src == nil {
			src = unresolved[name]
		}
		entries = append(entries, classify(name, ts, byType[name], src, problems[name], parentTypes, typeTemplate))
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Type < entries[j].Type })

	counts := map[string]int{}
	for _, e := range entries {
		counts[e.Class]++
		if e.Class == classIdentitySchema {
			counts["identity-schema/"+e.TemplateClass]++
		}
	}
	return &artifact{
		GeneratedBy: "go run ./tools/azurerm-id-gen -src <azurerm checkout> -schema <providers schema json>",
		Provider:    "hashicorp/azurerm",
		Pin:         pin{Version: version, Commit: commit, SchemaSHA256: digest},
		Counts:      counts,
		Types:       entries,
	}, nil
}

func classify(name string, ts typeSchema, r registration, src *idSource, problem string, parentTypes func([]segment) []string, typeTemplate func(string) (string, bool)) entry {
	e := entry{Type: name, IdentitySchema: ts.Identity, Via: r.Via}
	finish := func(class string) entry {
		if ts.Identity != nil {
			e.TemplateClass = class
			e.Class = classIdentitySchema
			return e
		}
		e.Class = class
		return e
	}
	e.Source = src
	if problem != "" {
		e.Reason = problem
		return finish(classUnmapped)
	}
	e.Template = template(src.Segments)
	m := mapSegments(src.Segments, mapCtx{
		Type:         name,
		Schema:       ts,
		SubForm:      subscriptionFormIn(r.File),
		ParentTypes:  parentTypes,
		TypeTemplate: typeTemplate,
	})
	if m.Reason != "" {
		e.Reason, e.Detail = m.Reason, m.Detail
		return finish(classUnmapped)
	}
	e.Components = m.Components
	if m.ParentID {
		return finish(classParentID)
	}
	return finish(classTemplate)
}

// readSchema loads the azurerm half of a providers-schema document and
// digests it. The digest is over the re-encoded azurerm object (Go sorts
// map keys), so it pins the provider's schema rather than the byte layout
// of whichever tofu wrote the file or the other providers beside it.
func readSchema(path string) (map[string]typeSchema, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	var doc struct {
		ProviderSchemas map[string]json.RawMessage `json:"provider_schemas"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, "", err
	}
	raw, ok := doc.ProviderSchemas[azurermSchemaKey]
	if !ok {
		return nil, "", fmt.Errorf("%s has no %s schema", path, azurermSchemaKey)
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, "", err
	}
	canon, err := json.Marshal(generic)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(canon)

	var p struct {
		ResourceSchemas map[string]struct {
			Block struct {
				Attributes map[string]struct {
					Required bool `json:"required"`
					Optional bool `json:"optional"`
					Computed bool `json:"computed"`
				} `json:"attributes"`
			} `json:"block"`
		} `json:"resource_schemas"`
		ResourceIdentitySchemas map[string]struct {
			Attributes map[string]json.RawMessage `json:"attributes"`
		} `json:"resource_identity_schemas"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, "", err
	}
	out := map[string]typeSchema{}
	for name, rs := range p.ResourceSchemas {
		ts := typeSchema{Attrs: map[string]attrInfo{}}
		for an, a := range rs.Block.Attributes {
			ts.Attrs[an] = attrInfo{Required: a.Required, Optional: a.Optional, Computed: a.Computed}
		}
		if is, ok := p.ResourceIdentitySchemas[name]; ok {
			ts.Identity = []string{}
			for an := range is.Attributes {
				ts.Identity = append(ts.Identity, an)
			}
			sort.Strings(ts.Identity)
		}
		out[name] = ts
	}
	return out, hex.EncodeToString(sum[:]), nil
}

// sourcePin reads the checkout's version/VERSION and its git HEAD. Either
// may be absent (a fixture tree has neither) and is then recorded empty.
func sourcePin(src string) (string, string) {
	version := ""
	if data, err := os.ReadFile(filepath.Join(src, "version", "VERSION")); err == nil {
		version = strings.TrimSpace(string(data))
	}
	commit := ""
	if out, err := exec.Command("git", "-C", src, "rev-parse", "HEAD").Output(); err == nil {
		commit = strings.TrimSpace(string(out))
		// A fixture under testdata sits inside this repository's own git
		// tree; its HEAD is not the source's.
		if top, err := exec.Command("git", "-C", src, "rev-parse", "--show-toplevel").Output(); err == nil {
			abs, _ := filepath.Abs(src)
			if real, err := filepath.EvalSymlinks(abs); err == nil {
				abs = real
			}
			if strings.TrimSpace(string(top)) != abs {
				commit = ""
			}
		}
	}
	return version, commit
}

func printSummary(w *os.File, a *artifact) {
	fmt.Fprintf(w, "azurerm %s (%s): %d resource types\n", a.Pin.Version, a.Pin.Commit, len(a.Types))
	var keys []string
	for k := range a.Counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "  %-34s %5d\n", k, a.Counts[k])
	}
	reasons := map[string]int{}
	for _, e := range a.Types {
		if e.Reason != "" {
			reasons[e.Reason]++
		}
	}
	type rc struct {
		r string
		n int
	}
	var rs []rc
	for r, n := range reasons {
		rs = append(rs, rc{r, n})
	}
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].n != rs[j].n {
			return rs[i].n > rs[j].n
		}
		return rs[i].r < rs[j].r
	})
	if len(rs) > 20 {
		rs = rs[:20]
	}
	fmt.Fprintln(w, "largest unmapped reasons (including identity-schema rows whose template is unmapped):")
	for _, r := range rs {
		fmt.Fprintf(w, "  %5d  %s\n", r.n, r.r)
	}
}
