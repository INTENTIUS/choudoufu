// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package armtypes

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// GeneratedDir is where azapi keeps its embedded bicep-types data,
// relative to the provider repository's root.
const GeneratedDir = "internal/azure/generated"

// bicep-types property flags (bicep-types' ObjectTypePropertyFlags, which
// azapi mirrors as ObjectPropertyFlag in internal/azure/types/object_type.go).
const (
	flagRequired           = 1 << 0
	flagReadOnly           = 1 << 1
	flagWriteOnly          = 1 << 2
	flagDeployTimeConstant = 1 << 3
)

type bicepRef struct {
	Ref string `json:"$ref"`
}

type bicepProp struct {
	Flags       int    `json:"flags"`
	Description string `json:"description"`
}

// bicepType decodes only the fields this table reads. The rest of a type
// (descriptions, element types, scopes) is irrelevant to a property's
// presence and flags.
type bicepType struct {
	Type           string               `json:"$type"`
	Body           *bicepRef            `json:"body"`
	Properties     map[string]bicepProp `json:"properties"`
	BaseProperties map[string]bicepProp `json:"baseProperties"`
}

// SourcePin reads the tag and commit an azapi checkout is at, the pair the
// table records. The checkout must sit exactly on a tag: a table generated
// from an untagged commit could not say which release it describes.
func SourcePin(repoDir string) (version, commit string, err error) {
	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", repoDir}, args...)...).Output()
		if err != nil {
			return "", fmt.Errorf("git %s in %s: %w", strings.Join(args, " "), repoDir, err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	if commit, err = git("rev-parse", "HEAD"); err != nil {
		return "", "", err
	}
	if version, err = git("describe", "--tags", "--exact-match", "HEAD"); err != nil {
		return "", "", fmt.Errorf("%s is not checked out at a release tag: %w", repoDir, err)
	}
	return version, commit, nil
}

// Generate builds the table from an azapi checkout's embedded type data.
//
// For every "<type>@<api-version>" key in index.json it loads the
// ResourceType the key references, resolves its body, and reads three
// properties off it with azapi's canResourceHaveProperty test (present,
// ReadOnly flag clear), counting a DiscriminatedObjectType body's
// baseProperties as the package doc explains. Anything azapi itself could
// not load - a non-ResourceType entry, a body reference that is not
// in-file - is an error here rather than a silently absent row, so a pin
// bump that changes the data's shape fails loudly.
func Generate(repoDir, version, commit string) (*Table, error) {
	tbl := &Table{AzapiVersion: version, AzapiCommit: commit, rows: map[string]*typeRow{}}
	type entry struct {
		version string
		caps    Caps
	}
	byType := map[string]map[string]Caps{}
	err := walkBodies(repoDir, func(typ, ver string, discriminated bool, props map[string]bicepProp) error {
		var caps Caps
		for name, bit := range map[string]Caps{"tags": CapTags, "name": CapName, "location": CapLocation} {
			if p, ok := props[name]; ok && p.Flags&flagReadOnly == 0 {
				caps |= bit
			}
		}
		if discriminated && caps.Has(CapTags) {
			tbl.DiscriminatedTags++
			caps |= CapTagsViaBase
		}

		lower := strings.ToLower(typ)
		if byType[lower] == nil {
			byType[lower] = map[string]Caps{}
		}
		if prev, dup := byType[lower][ver]; dup {
			// Two index keys differing only by case. azapi's lookup
			// would pick one by map order; refuse unless they agree.
			if prev != caps {
				return fmt.Errorf("%s@%s appears twice with different capabilities (%d, %d)", lower, ver, prev, caps)
			}
			return nil
		}
		byType[lower][ver] = caps
		return nil
	})
	if err != nil {
		return nil, err
	}

	for typ, vs := range byType {
		es := make([]entry, 0, len(vs))
		for v, c := range vs {
			es = append(es, entry{v, c})
		}
		sort.Slice(es, func(i, j int) bool { return es[i].version < es[j].version })
		r := &typeRow{}
		for _, e := range es {
			r.versions = append(r.versions, e.version)
			r.caps = append(r.caps, e.caps)
		}
		tbl.rows[typ] = r
	}
	return tbl, nil
}

// walkBodies loads every "<type>@<api-version>" in azapi's index.json,
// resolves its ResourceType's body, and hands visit the body's properties
// in key order: an ObjectType's properties, or a DiscriminatedObjectType's
// baseProperties (discriminated true). Anything azapi itself could not
// load - a non-ResourceType entry, a body reference that is not in-file -
// is an error rather than a silently absent row, so a pin bump that
// changes the data's shape fails loudly.
func walkBodies(repoDir string, visit func(typ, ver string, discriminated bool, props map[string]bicepProp) error) error {
	root := filepath.Join(repoDir, filepath.FromSlash(GeneratedDir))
	var index struct {
		Resources map[string]bicepRef `json:"resources"`
	}
	if err := readJSON(filepath.Join(root, "index.json"), &index); err != nil {
		return err
	}
	if len(index.Resources) == 0 {
		return fmt.Errorf("%s/index.json lists no resources", GeneratedDir)
	}

	files := map[string][]json.RawMessage{}
	load := func(file string, i int) (*bicepType, error) {
		types, ok := files[file]
		if !ok {
			if err := readJSON(filepath.Join(root, filepath.FromSlash(file)), &types); err != nil {
				return nil, err
			}
			files[file] = types
		}
		if i < 0 || i >= len(types) {
			return nil, fmt.Errorf("%s: index %d out of range (%d types)", file, i, len(types))
		}
		var t bicepType
		if err := json.Unmarshal(types[i], &t); err != nil {
			return nil, fmt.Errorf("%s#/%d: %w", file, i, err)
		}
		return &t, nil
	}

	keys := make([]string, 0, len(index.Resources))
	for k := range index.Resources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		// azapi splits at the first "@" (internal/azure/index.go,
		// Schema.UnmarshalJSON); a second one would be ambiguous.
		typ, ver, ok := strings.Cut(key, "@")
		if !ok || strings.Contains(ver, "@") || typ == "" || ver == "" {
			return fmt.Errorf("index.json key %q is not <type>@<api-version>", key)
		}
		file, idx, err := splitRef(index.Resources[key].Ref)
		if err != nil {
			return fmt.Errorf("index.json %q: %w", key, err)
		}
		if file == "" {
			file = "index.json"
		}
		rt, err := load(file, idx)
		if err != nil {
			return err
		}
		if rt.Type != "ResourceType" || rt.Body == nil {
			return fmt.Errorf("index.json %q references a %s, not a ResourceType with a body", key, rt.Type)
		}
		bfile, bidx, err := splitRef(rt.Body.Ref)
		if err != nil || bfile != "" {
			// azapi resolves only "#/N" (types/type_reference.go,
			// UpdateType); any other body reference leaves its body
			// nil, and canResourceHaveProperty false.
			return fmt.Errorf("%s#/%d (%s): body reference %q is not in-file", file, idx, key, rt.Body.Ref)
		}
		body, err := load(file, bidx)
		if err != nil {
			return err
		}
		switch body.Type {
		case "ObjectType":
			err = visit(typ, ver, false, body.Properties)
		case "DiscriminatedObjectType":
			err = visit(typ, ver, true, body.BaseProperties)
		default:
			return fmt.Errorf("%s#/%d (%s): body is a %s", file, bidx, key, body.Type)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// splitRef splits a bicep-types reference, "file.json#/N" or "#/N", into
// its file (empty when in-file) and index.
func splitRef(ref string) (file string, idx int, err error) {
	file, frag, ok := strings.Cut(ref, "#/")
	if !ok {
		return "", 0, fmt.Errorf("reference %q has no #/ index", ref)
	}
	idx, err = strconv.Atoi(frag)
	if err != nil {
		return "", 0, fmt.Errorf("reference %q: %w", ref, err)
	}
	if file != "" {
		file = path.Clean(file)
	}
	return file, idx, nil
}

func readJSON(file string, v any) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	return nil
}
