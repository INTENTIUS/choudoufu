// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package investigate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// TemplatesDataVersion is the version of chant's templates.json shape this
// package reads (INTENTIUS/chant#4531, TEMPLATES_DATA_VERSION).
const TemplatesDataVersion = 1

// Query kinds a template expands to.
const (
	QueryPath   = "path"
	QueryCypher = "cypher"
)

// Input kinds.
const (
	InputResource = "resource"
	InputScope    = "scope"
	InputTerm     = "term"
)

// Template is one investigation template from chant's catalog, read as data.
// The fields are chant's TemplateEntry (packages/core/src/capabilities/
// templates.ts) under the same JSON names.
type Template struct {
	ID       string          `json:"id"`
	Title    string          `json:"title"`
	Question string          `json:"question"`
	Sources  []string        `json:"sources,omitempty"`
	Inputs   []TemplateInput `json:"inputs"`
	Reads    string          `json:"reads"`
	Query    TemplateQuery   `json:"query"`
	Note     string          `json:"note,omitempty"`
}

// TemplateInput is one typed input of a template.
type TemplateInput struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
}

// TemplateQuery is the query a template expands to.
type TemplateQuery struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Resource is the name of the template's one resource input. ParseTemplates
// guarantees it exists and comes first.
func (t Template) Resource() string {
	return t.Inputs[0].Name
}

// Catalog is the template catalog as this run found it.
type Catalog struct {
	// Templates are chant's investigation templates. Empty when Unavailable
	// is set.
	Templates []Template `json:"templates"`

	// Source says where the templates were read from: the file given, the
	// capabilities document of `chant search --capabilities --json`, or the
	// templates.json an installed chant ships.
	Source string `json:"source,omitempty"`

	// Chant is the version of chant that wrote the capabilities document,
	// when the catalog came from one.
	Chant string `json:"chant,omitempty"`

	// Unavailable says why there is no catalog, in words a person can act on.
	Unavailable string `json:"unavailable,omitempty"`
}

// Template returns the template with the given id.
func (c Catalog) Template(id string) (Template, bool) {
	for _, t := range c.Templates {
		if t.ID == id {
			return t, true
		}
	}
	return Template{}, false
}

var (
	templateID  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	inputName   = regexp.MustCompile(`^[a-z][A-Za-z0-9_]*$`)
	placeholder = regexp.MustCompile(`\{\{([^{}]*)\}\}`)
	cypherParam = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]*)`)
	cypherRead  = regexp.MustCompile(`(?i)^\s*(MATCH|OPTIONAL MATCH|WITH|CALL|UNWIND)\b`)
	cypherWrite = regexp.MustCompile(`(?i)\b(CREATE|MERGE|DELETE|DETACH|SET|REMOVE|DROP)\b`)
)

var validReads = map[string]bool{"source": true, "live": true, "snapshots": true, "session": true}

// ParseTemplates reads a templates.json document and checks every entry the
// way chant's parseTemplates does: a unique id, one resource input and it
// first, a path pattern whose placeholders are its inputs, or a Cypher query
// that only reads and takes its inputs as parameters. choudoufu holds no copy
// of the catalog, so a file that does not hold to the entry kind is refused
// whole, naming the entry, rather than half-used.
func ParseTemplates(data []byte) ([]Template, error) {
	var doc struct {
		Version   *int              `json:"version"`
		Templates []json.RawMessage `json:"templates"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("the templates file is not JSON: %w", err)
	}
	if doc.Version == nil || *doc.Version != TemplatesDataVersion {
		v := "absent"
		if doc.Version != nil {
			v = fmt.Sprint(*doc.Version)
		}
		return nil, fmt.Errorf("templates file version %s is not %d, the version this choudoufu reads", v, TemplatesDataVersion)
	}
	if doc.Templates == nil {
		return nil, errors.New("the templates file has no templates list")
	}
	return checkTemplates(doc.Templates)
}

func checkTemplates(raw []json.RawMessage) ([]Template, error) {
	seen := map[string]bool{}
	out := make([]Template, 0, len(raw))
	for i, r := range raw {
		var t Template
		if err := json.Unmarshal(r, &t); err != nil {
			return nil, fmt.Errorf("template %d: %w", i, err)
		}
		where := fmt.Sprintf("template %d", i)
		if t.ID != "" {
			where = "template " + t.ID
		}
		fail := func(format string, a ...any) error {
			return fmt.Errorf("%s: %s", where, fmt.Sprintf(format, a...))
		}
		if !templateID.MatchString(t.ID) {
			return nil, fail("needs an id of lowercase letters, digits and -")
		}
		if seen[t.ID] {
			return nil, fail("is defined twice")
		}
		seen[t.ID] = true
		if strings.TrimSpace(t.Title) == "" {
			return nil, fail("needs a title")
		}
		if strings.TrimSpace(t.Question) == "" {
			return nil, fail("needs the question it answers")
		}
		if !validReads[t.Reads] {
			return nil, fail("reads is one of source, live, snapshots, session")
		}
		names := map[string]bool{}
		resources := 0
		for _, in := range t.Inputs {
			if !inputName.MatchString(in.Name) {
				return nil, fail("an input needs a name")
			}
			if names[in.Name] {
				return nil, fail("input %s is listed twice", in.Name)
			}
			names[in.Name] = true
			switch in.Kind {
			case InputResource:
				resources++
			case InputScope, InputTerm:
			default:
				return nil, fail("input %s's kind is one of resource, scope, term", in.Name)
			}
		}
		if resources != 1 || len(t.Inputs) == 0 || t.Inputs[0].Kind != InputResource {
			return nil, fail("takes one resource input, first")
		}
		if strings.TrimSpace(t.Query.Text) == "" {
			return nil, fail("query is { kind: path | cypher, text }")
		}
		switch t.Query.Kind {
		case QueryPath:
			if regexp.MustCompile(`\$[A-Za-z_]`).MatchString(t.Query.Text) {
				return nil, fail("a path pattern takes {{input}} placeholders, not $parameters")
			}
			used := map[string]bool{}
			for _, m := range placeholder.FindAllStringSubmatch(t.Query.Text, -1) {
				if !names[m[1]] {
					return nil, fail("{{%s}} names no input", m[1])
				}
				used[m[1]] = true
			}
			for _, in := range t.Inputs {
				if in.Kind != InputScope && !used[in.Name] {
					return nil, fail("the path pattern never uses %s", in.Name)
				}
			}
		case QueryCypher:
			if strings.Contains(t.Query.Text, "{{") {
				return nil, fail("a Cypher query takes $parameters, never spliced {{input}} text")
			}
			if !cypherRead.MatchString(t.Query.Text) {
				return nil, fail("a Cypher query starts with a reading clause")
			}
			if cypherWrite.MatchString(t.Query.Text) {
				return nil, fail("a Cypher query only reads")
			}
			params := map[string]bool{}
			for _, m := range cypherParam.FindAllStringSubmatch(t.Query.Text, -1) {
				params[m[1]] = true
			}
			if !params["key"] {
				return nil, fail("a Cypher query binds the resource as $key")
			}
			for p := range params {
				if p != "key" && !names[p] {
					return nil, fail("$%s names no input", p)
				}
			}
			for _, in := range t.Inputs[1:] {
				if in.Kind != InputScope && !params[in.Name] {
					return nil, fail("the query never uses $%s", in.Name)
				}
			}
		default:
			return nil, fail("query is { kind: path | cypher, text }")
		}
		out = append(out, t)
	}
	return out, nil
}

// LoadCatalog finds chant's investigation templates. In order:
//
//  1. the file Tools.TemplatesFile names, when one is given;
//  2. the templates on the capabilities document `chant search
//     --capabilities --json` prints (chant 0.124.53 and newer carry them);
//  3. the templates.json the installed chant package ships
//     (@intentius/chant/capabilities/templates.json), found from the chant
//     binary, for a chant whose capabilities document predates them.
//
// With none of these, the catalog is Unavailable with the reason. chant is
// optional: the native lookups never need it.
func LoadCatalog(ctx context.Context, t *Tools) Catalog {
	if t.TemplatesFile != "" {
		data, err := os.ReadFile(t.TemplatesFile)
		if err != nil {
			return Catalog{Unavailable: fmt.Sprintf("cannot read the templates file %s: %s", t.TemplatesFile, err)}
		}
		tpls, err := ParseTemplates(data)
		if err != nil {
			return Catalog{Unavailable: fmt.Sprintf("%s: %s", t.TemplatesFile, err)}
		}
		return Catalog{Templates: tpls, Source: t.TemplatesFile}
	}

	chant := t.ChantPath()
	if chant == "" {
		return Catalog{Unavailable: "no chant binary on PATH. The investigation templates are chant's catalog (@intentius/chant/capabilities/templates.json, INTENTIUS/chant#4531): install chant, or pass -templates=<file> with a copy of that file. The native lookups (owner, history, foreign, affected) still work."}
	}

	var capsErr error
	out, err := t.runChant(ctx, "search", "--capabilities", "--json")
	if err == nil {
		var doc struct {
			Chant     string            `json:"chant"`
			Templates []json.RawMessage `json:"templates"`
			Error     *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		switch jerr := json.Unmarshal(out, &doc); {
		case jerr != nil:
			capsErr = fmt.Errorf("chant search --capabilities --json printed something that is not JSON: %w", jerr)
		case doc.Error != nil:
			capsErr = fmt.Errorf("chant search --capabilities refused: %s", doc.Error.Message)
		case doc.Templates != nil:
			tpls, perr := checkTemplates(doc.Templates)
			if perr != nil {
				return Catalog{Unavailable: fmt.Sprintf("the templates on chant %s's capabilities document do not hold to the entry kind: %s", doc.Chant, perr)}
			}
			return Catalog{Templates: tpls, Source: "chant search --capabilities --json", Chant: doc.Chant}
		default:
			capsErr = fmt.Errorf("chant %s's capabilities document carries no templates (chant 0.124.53 is the first that does)", doc.Chant)
		}
	} else {
		capsErr = err
	}

	if file := shippedTemplatesFile(chant); file != "" {
		data, err := os.ReadFile(file)
		if err == nil {
			tpls, perr := ParseTemplates(data)
			if perr != nil {
				return Catalog{Unavailable: fmt.Sprintf("%s: %s", file, perr)}
			}
			return Catalog{Templates: tpls, Source: file}
		}
	}
	return Catalog{Unavailable: fmt.Sprintf("%s, and the installed chant ships no templates.json beside it. Upgrade chant, or pass -templates=<file>.", capsErr)}
}

// shippedTemplatesFile finds src/capabilities/templates.json in the
// @intentius/chant package the chant binary belongs to: the binary is a
// symlink into the package's bin/ (npm's node_modules/.bin/chant), so the
// package is the first directory above it with a package.json naming
// @intentius/chant.
func shippedTemplatesFile(chant string) string {
	resolved, err := filepath.EvalSymlinks(chant)
	if err != nil {
		return ""
	}
	dir := filepath.Dir(resolved)
	for i := 0; i < 6; i++ {
		if data, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
			var pkg struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(data, &pkg) == nil && pkg.Name == "@intentius/chant" {
				file := filepath.Join(dir, "src", "capabilities", "templates.json")
				if _, err := os.Stat(file); err == nil {
					return file
				}
				return ""
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}
