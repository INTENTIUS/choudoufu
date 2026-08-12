// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/configs/configschema"
	"github.com/opentofu/opentofu/internal/lang/marks"
	"github.com/opentofu/opentofu/internal/providers"
	"github.com/opentofu/opentofu/internal/states"
	"github.com/opentofu/opentofu/internal/tofu"
)

// The three marker tag keys this package looks for when reading a resource
// instance object's tags back into [snapshotResource.Markers]. Copied from
// stateless/MARKERS.md rather than imported from internal/stateless/discovery,
// which defines the same three constants: discovery's test suite imports
// this package (to drive floci integration tests against a built
// projection), so this package importing discovery in return would be an
// import cycle. Three short string constants are cheaper to keep in sync by
// hand than a package boundary redesign.
const (
	markerTagEstate  = "tofu-estate"
	markerTagAddress = "tofu-address"
	markerTagSlot    = "tofu-slot"
)

// snapshotFormatVersion identifies the JSON shape [snapshot] writes. It is
// not a tfstate format and is not versioned alongside one; it is this
// fork's own cache shape, free to change without touching anything state
// format compatibility depends on.
//
// It is unexported, and so are [snapshot] and [snapshotResource], as the
// first half of P4.2 rule 3's enforcement: no package outside this one can
// name the format constant or decode the file into its declared shape, and
// that is the compiler's judgement rather than a convention. The second half
// is [TestSnapshot_noReader], which sweeps every .go file in the repository -
// this package's own included - for the literal value and for a redeclared
// copy of the struct shape, because an unexported identifier is a wall a
// hand-rolled struct can still walk around.
//
// Neither half can catch a reader that opens the path and decodes into
// map[string]any without naming anything. That residue is stated honestly
// in stateless/ docs rather than papered over: enforcement is real for
// typed readers and a convention for untyped ones.
//
// Bumped from "tofu-stateless-snapshot-v1" to "tofu-live-snapshot-v1" by
// PN.1, the "live" rename: the value is free to move because, by the same
// no-reader guarantee this comment describes, nothing anywhere parses it
// back, so there is no format to migrate and no old snapshot file that ever
// needs to be understood again.
const snapshotFormatVersion = "tofu-live-snapshot-v1"

// snapshot is the optional, observational post-operation cache (P4.2): a
// scrubbed, metadata-only record of what a stateless run's projection held
// when its state was last persisted, written for offline diff and audit.
//
// Everything in it is metadata, a one-way hash, or a value redaction has
// been offered a chance to remove. No resource's attribute values appear
// here in bulk, by construction rather than by discipline - see
// AttributesHash - and the handful of individual values that do appear
// (an import ID, an identity, the ownership marker tags) are run through
// [sensitivity] first, so a value the plan renderer would print as
// "(sensitive value)" is not written here in the clear either. See
// [objectSensitivity] for what "sensitive" is decided from.
//
// Nothing in stateless mode opens this file to read it back; see the manager's
// PersistState for where it is written and why that is the only such place.
type snapshot struct {
	// FormatVersion is always [snapshotFormatVersion].
	FormatVersion string `json:"formatVersion"`

	// WrittenAt is when this snapshot was built, RFC3339, from the clock the
	// manager was configured with (real time in production, injected in
	// tests). It is the staleness signal: anything reading this file - which
	// nothing in stateless mode does - has to treat WrittenAt as "as of", not
	// "as of now".
	WrittenAt string `json:"writtenAt"`

	// Estate is the estate name this run resolved, the same value stamped
	// into the tofu-estate marker on the live resources.
	Estate string `json:"estate"`

	// Resources is every current resource instance object the projection
	// held, in address order. Deposed objects (pending destruction under
	// create_before_destroy) are not included: they are not part of the
	// estate's current shape.
	Resources []snapshotResource `json:"resources"`
}

// snapshotResource is one resource instance's entry in [snapshot].
type snapshotResource struct {
	// Address is the resource instance's absolute address, e.g.
	// aws_vpc.main or aws_eip.pool[0].
	Address string `json:"address"`

	// Type is the resource type, e.g. aws_vpc.
	Type string `json:"type"`

	// ImportID is the object's import-style identity string (its "id"
	// attribute), used when the object carries no structured identity.
	// Mutually exclusive with Identity. Redacted when the "id" attribute is
	// sensitive.
	ImportID string `json:"importID,omitempty"`

	// Identity is the object's provider-native identity (core's #2854
	// plumbing), decoded from the state's IdentityJSON, when the object has
	// one. Mutually exclusive with ImportID. Identity attributes the
	// provider's identity schema declares sensitive are redacted.
	Identity map[string]any `json:"identity,omitempty"`

	// Markers is the subset of the object's tags that are stateless
	// ownership markers (tofu-estate, tofu-address, tofu-slot), read back
	// from the object as they were last observed. It is not the object's
	// full tag set: only the marker keys, so the snapshot cannot leak an
	// operator's arbitrary tag values either, and a marker whose own value
	// is sensitive is redacted rather than written.
	Markers map[string]string `json:"markers,omitempty"`

	// AttributesHash is a SHA-256 hex digest of the object's full attributes
	// JSON, present so that an offline diff can tell "this object changed"
	// from "this object did not", without the snapshot ever holding the
	// values that changed. This is the scrubbing-by-design guarantee for the
	// attribute blob: there is no field on this type the blob could be
	// assigned to, only the hash of it.
	AttributesHash string `json:"attributesHash"`
}

// buildSnapshot renders state into a [snapshot], attributed to estate and
// timestamped writtenAt. It never fails: a nil or empty state produces a
// snapshot with no resources, which is a legitimate answer (an empty
// estate, or a persist before anything was written).
//
// schemas is the provider schema set the operation is running with, and may
// be nil - a caller with no schemas in hand still gets a snapshot, built
// from the sensitivity marks the state itself recorded. It is not decorative
// when present: a provider marks attributes sensitive in its schema, and
// only the schema says which of the values reachable from this object (an
// id, an identity field, a tag) were ones the plan renderer would have
// printed as "(sensitive value)". Passing it is what makes the redaction
// below complete rather than best-effort. See [objectSensitivity].
func buildSnapshot(state *states.State, estate string, writtenAt time.Time, schemas *tofu.Schemas) *snapshot {
	snap := &snapshot{
		FormatVersion: snapshotFormatVersion,
		WrittenAt:     writtenAt.UTC().Format(time.RFC3339),
		Estate:        estate,
	}
	if state == nil {
		return snap
	}

	var current []addrs.AbsResourceInstance
	for _, rec := range state.AllResourceInstanceObjectAddrs() {
		if rec.DeposedKey != states.NotDeposed {
			continue
		}
		current = append(current, rec.Instance)
	}
	sort.Slice(current, func(i, j int) bool {
		return current[i].String() < current[j].String()
	})

	for _, addr := range current {
		ri := state.ResourceInstance(addr)
		if ri == nil || ri.Current == nil {
			continue
		}
		obj := ri.Current
		sens := objectSensitivity(obj, resourceSchema(state, addr, schemas))

		res := snapshotResource{
			Address:        sens.scrub(addr.String()),
			Type:           addr.Resource.Resource.Type,
			Markers:        sens.scrubMarkers(markersFromAttrs(obj.AttrsJSON)),
			AttributesHash: hashAttrs(obj.AttrsJSON),
		}
		if identity := decodeIdentity(obj.IdentityJSON); identity != nil {
			res.Identity = sens.scrubIdentity(identity)
		} else if sens.id {
			res.ImportID = redacted
		} else {
			res.ImportID = sens.scrub(importIDFromAttrs(obj.AttrsJSON))
		}
		snap.Resources = append(snap.Resources, res)
	}

	return snap
}

// resourceSchema looks up the schema for one instance's resource type,
// through the provider the state records for its containing resource rather
// than the one the type name suggests: a resource can name a non-default
// provider, and asking the wrong one yields no schema and therefore no
// sensitivity information, which is the failure mode redaction can least
// afford to have silently.
func resourceSchema(state *states.State, addr addrs.AbsResourceInstance, schemas *tofu.Schemas) *providers.Schema {
	if schemas == nil {
		return nil
	}
	res := state.Resource(addr.ContainingResource())
	if res == nil {
		return nil
	}
	schema, _ := schemas.ResourceTypeConfig(
		res.ProviderConfig.Provider,
		addr.Resource.Resource.Mode,
		addr.Resource.Resource.Type,
	)
	return schema
}

// markersFromAttrs reads the stateless ownership marker tags (and only
// those) out of a resource instance object's attributes JSON. The object's
// "tags" attribute is read in preference to "tags_all", matching how the
// stamping and discovery packages resolve the same ambiguity - an
// explicitly configured tag wins over one arriving through a provider's
// default_tags.
func markersFromAttrs(attrsJSON []byte) map[string]string {
	if len(attrsJSON) == 0 {
		return nil
	}
	var attrs map[string]any
	if err := json.Unmarshal(attrsJSON, &attrs); err != nil {
		return nil
	}

	var tags map[string]any
	if v, ok := attrs["tags"].(map[string]any); ok {
		tags = v
	} else if v, ok := attrs["tags_all"].(map[string]any); ok {
		tags = v
	}
	if len(tags) == 0 {
		return nil
	}

	var markers map[string]string
	for _, key := range []string{markerTagEstate, markerTagAddress, markerTagSlot} {
		if v, ok := tags[key].(string); ok {
			if markers == nil {
				markers = make(map[string]string, 3)
			}
			markers[key] = v
		}
	}
	return markers
}

// importIDFromAttrs reads an object's "id" attribute, the conventional
// import-style identity every admitted resource type in stateless mode's subset
// carries. Used as SnapshotResource.ImportID when the object has no
// structured IdentityJSON to decode instead.
func importIDFromAttrs(attrsJSON []byte) string {
	if len(attrsJSON) == 0 {
		return ""
	}
	var attrs map[string]any
	if err := json.Unmarshal(attrsJSON, &attrs); err != nil {
		return ""
	}
	if id, ok := attrs["id"].(string); ok {
		return id
	}
	return ""
}

// decodeIdentity decodes a resource instance object's IdentityJSON, or
// returns nil when there isn't one (schema version 0 of this field, or a
// provider that has not adopted #2854 identity yet).
func decodeIdentity(identityJSON []byte) map[string]any {
	if len(identityJSON) == 0 {
		return nil
	}
	var identity map[string]any
	if err := json.Unmarshal(identityJSON, &identity); err != nil {
		return nil
	}
	if len(identity) == 0 {
		return nil
	}
	return identity
}

// hashAttrs is the one-way side of the scrubbing-by-design guarantee: a
// digest of the full attributes blob, useful for "did this change" without
// ever holding what changed.
func hashAttrs(attrsJSON []byte) string {
	sum := sha256.Sum256(attrsJSON)
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// Redaction
// ---------------------------------------------------------------------------

// redacted is what a sensitive value becomes in a snapshot. It is the same
// string the plan renderer prints for the same value on purpose: an operator
// reading the snapshot next to a plan should not have to learn a second
// vocabulary for "this was withheld".
const redacted = "(sensitive value)"

// minSweptSecret is the shortest sensitive string [sensitivity.scrub] will
// hunt for in the handful of values a snapshot entry records.
//
// The sweep is defence in depth, not the primary mechanism: the paths a
// snapshot actually writes (the id attribute, the marker tags, the identity
// attributes) are redacted from their paths, with no length rule at all. The
// sweep exists for the case where a sensitive value reached one of those
// fields by a route the schema does not describe - a for_each key derived
// from a secret ending up inside a tofu-address marker, say.
//
// Below eight characters a value is not a credential in any sense worth
// defending, while a short substring match would mangle unrelated
// identifiers - "true", "1", or a two-letter region code would rewrite half
// the file. So the sweep declines to guess there and the path-based
// redaction, which does not guess at all, is what covers those.
const minSweptSecret = 8

// sensitivity is what one resource instance object's sensitivity marks and
// provider schema say about the specific values a snapshot entry records.
// Everything else in the object is already reduced to a hash, so this is a
// deliberately small vocabulary rather than a general marks engine.
type sensitivity struct {
	// id is true when the object's "id" attribute is sensitive, which makes
	// the entry's ImportID unwritable.
	id bool

	// tags is true when the whole tags (or tags_all) map is sensitive, which
	// makes every marker value unwritable.
	tags bool

	// tagKeys names individual tag keys whose values are sensitive.
	tagKeys map[string]bool

	// identity names identity attributes the provider's identity schema
	// declares sensitive.
	identity map[string]bool

	// values is every sensitive string found anywhere in the object, for the
	// substring sweep described on minSweptSecret.
	values []string
}

// objectSensitivity reads an instance object's sensitivity from the two
// places that know about it, which answer different questions and are both
// needed.
//
// The state's own AttrSensitivePaths records the marks that survived the
// last write - what OpenTofu decided was sensitive for this object, including
// sensitivity that arrived from a sensitive input variable and is invisible
// to any schema. The provider's schema declares which attributes are
// sensitive by their nature, which covers every object whose marks were
// dropped or never recorded (an object read back by the projection's import
// path has no marks at all, and that is exactly the object stateless mode writes
// snapshots of).
//
// schema may be nil, in which case only the marks are consulted.
func objectSensitivity(obj *states.ResourceInstanceObjectSrc, schema *providers.Schema) sensitivity {
	s := sensitivity{
		tagKeys:  map[string]bool{},
		identity: map[string]bool{},
	}

	var attrs map[string]any
	if len(obj.AttrsJSON) > 0 {
		// A blob that will not decode carries no readable values either, so
		// there is nothing to redact and nothing to report.
		_ = json.Unmarshal(obj.AttrsJSON, &attrs)
	}
	var ident map[string]any
	if len(obj.IdentityJSON) > 0 {
		_ = json.Unmarshal(obj.IdentityJSON, &ident)
	}

	for _, pvm := range obj.AttrSensitivePaths {
		if _, ok := pvm.Marks[marks.Sensitive]; !ok {
			continue
		}
		s.notePath(pvm.Path)
		if v, ok := valueAtPath(attrs, pvm.Path); ok {
			s.noteValues(v)
		}
	}

	if schema != nil {
		if schema.Block != nil {
			s.noteBlock(schema.Block, attrs)
		}
		if schema.IdentitySchema != nil {
			for name, attrS := range schema.IdentitySchema.Attributes {
				if attrS.Sensitive {
					s.identity[name] = true
					s.noteValues(ident[name])
					continue
				}
				if attrS.NestedType != nil && attrS.NestedType.ContainsSensitive() {
					s.noteObject(attrS.NestedType, ident[name])
				}
			}
		}
	}

	return s
}

// notePath records what one sensitive path means for the fields a snapshot
// entry writes. Paths that do not reach any of them are still useful - their
// values go into the sweep - so an unrecognized shape is not an error here.
func (s *sensitivity) notePath(p cty.Path) {
	switch len(p) {
	case 1:
		if step, ok := p[0].(cty.GetAttrStep); ok {
			s.noteName(step.Name)
		}
	case 2:
		outer, ok := p[0].(cty.GetAttrStep)
		if !ok || (outer.Name != "tags" && outer.Name != "tags_all") {
			return
		}
		idx, ok := p[1].(cty.IndexStep)
		if !ok || idx.Key.Type() != cty.String || idx.Key.IsNull() {
			return
		}
		s.tagKeys[idx.Key.AsString()] = true
	}
}

// noteName records a sensitive top-level attribute by name.
func (s *sensitivity) noteName(name string) {
	switch name {
	case "id":
		s.id = true
	case "tags", "tags_all":
		s.tags = true
	}
}

// noteBlock walks a resource type's configuration schema alongside the
// object's decoded attributes, recording every sensitive attribute by name
// and collecting its values for the sweep. Nested attribute types and nested
// blocks are descended, so a secret buried in a nested block is found too.
func (s *sensitivity) noteBlock(b *configschema.Block, v map[string]any) {
	for name, attrS := range b.Attributes {
		if attrS.Sensitive {
			s.noteName(name)
			s.noteValues(v[name])
			continue
		}
		if attrS.NestedType != nil && attrS.NestedType.ContainsSensitive() {
			s.noteObject(attrS.NestedType, v[name])
		}
	}
	for name, blockS := range b.BlockTypes {
		if !blockS.Block.ContainsSensitive() {
			continue
		}
		for _, nested := range nestedMaps(blockS.Nesting, v[name]) {
			s.noteBlock(&blockS.Block, nested)
		}
	}
}

// noteObject is noteBlock for a nested attribute type (and for the identity
// schema, which is one).
func (s *sensitivity) noteObject(o *configschema.Object, v any) {
	for _, nested := range nestedMaps(o.Nesting, v) {
		for name, attrS := range o.Attributes {
			if attrS.Sensitive {
				s.noteValues(nested[name])
				continue
			}
			if attrS.NestedType != nil && attrS.NestedType.ContainsSensitive() {
				s.noteObject(attrS.NestedType, nested[name])
			}
		}
	}
}

// nestedMaps turns the JSON shape of a nested block or attribute type into
// the object maps inside it, one per instance, according to its nesting mode.
// Anything that does not have the expected shape yields nothing, which for a
// redaction pass means "nothing found here", never a panic.
func nestedMaps(nesting configschema.NestingMode, v any) []map[string]any {
	switch nesting {
	case configschema.NestingSingle, configschema.NestingGroup:
		if m, ok := v.(map[string]any); ok {
			return []map[string]any{m}
		}
	case configschema.NestingList, configschema.NestingSet:
		l, ok := v.([]any)
		if !ok {
			return nil
		}
		out := make([]map[string]any, 0, len(l))
		for _, e := range l {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case configschema.NestingMap:
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		out := make([]map[string]any, 0, len(m))
		for _, e := range m {
			if em, ok := e.(map[string]any); ok {
				out = append(out, em)
			}
		}
		return out
	}
	return nil
}

// noteValues collects every string leaf under a sensitive value, so that the
// sweep can find the same string if it also turns up somewhere the snapshot
// writes.
func (s *sensitivity) noteValues(v any) {
	switch t := v.(type) {
	case string:
		if t != "" {
			s.values = append(s.values, t)
		}
	case map[string]any:
		for _, e := range t {
			s.noteValues(e)
		}
	case []any:
		for _, e := range t {
			s.noteValues(e)
		}
	}
}

// valueAtPath walks a cty.Path through decoded JSON, which is how a
// sensitivity mark recorded against the cty value is applied to the blob the
// state actually stores. Only the two step kinds a state path can contain are
// handled; anything else means "cannot follow", not "empty".
func valueAtPath(v any, p cty.Path) (any, bool) {
	for _, step := range p {
		switch st := step.(type) {
		case cty.GetAttrStep:
			m, ok := v.(map[string]any)
			if !ok {
				return nil, false
			}
			v, ok = m[st.Name]
			if !ok {
				return nil, false
			}
		case cty.IndexStep:
			if st.Key.IsNull() || !st.Key.IsKnown() {
				return nil, false
			}
			switch st.Key.Type() {
			case cty.String:
				m, ok := v.(map[string]any)
				if !ok {
					return nil, false
				}
				v, ok = m[st.Key.AsString()]
				if !ok {
					return nil, false
				}
			case cty.Number:
				l, ok := v.([]any)
				if !ok {
					return nil, false
				}
				idx, _ := st.Key.AsBigFloat().Int64()
				if idx < 0 || int(idx) >= len(l) {
					return nil, false
				}
				v = l[idx]
			default:
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return v, true
}

// scrub removes any sensitive value the sweep is willing to look for from a
// string the snapshot is about to write. See minSweptSecret for why the
// sweep has a floor and why the path-based redactions below do not.
func (s sensitivity) scrub(v string) string {
	if v == "" {
		return v
	}
	for _, secret := range s.values {
		if len(secret) < minSweptSecret {
			continue
		}
		v = strings.ReplaceAll(v, secret, redacted)
	}
	return v
}

// scrubMarkers applies the tag-level redactions: a sensitive tags map takes
// every marker with it, a sensitive individual tag takes its own value, and
// whatever survives still goes through the sweep.
func (s sensitivity) scrubMarkers(markers map[string]string) map[string]string {
	if len(markers) == 0 {
		return markers
	}
	for key, value := range markers {
		if s.tags || s.tagKeys[key] {
			markers[key] = redacted
			continue
		}
		markers[key] = s.scrub(value)
	}
	return markers
}

// scrubIdentity applies the identity schema's redactions to a decoded
// identity object and sweeps what is left.
func (s sensitivity) scrubIdentity(identity map[string]any) map[string]any {
	for key, value := range identity {
		if s.identity[key] {
			identity[key] = redacted
			continue
		}
		identity[key] = s.scrubAny(value)
	}
	return identity
}

// scrubAny is scrub over an arbitrary decoded JSON value, for the nested
// shapes an identity object may contain.
func (s sensitivity) scrubAny(v any) any {
	switch t := v.(type) {
	case string:
		return s.scrub(t)
	case map[string]any:
		for k, e := range t {
			t[k] = s.scrubAny(e)
		}
		return t
	case []any:
		for i, e := range t {
			t[i] = s.scrubAny(e)
		}
		return t
	default:
		return v
	}
}

// writeSnapshot encodes snap as indented JSON and writes it to path
// atomically: a temp file in the same directory, written and closed, then
// renamed over the target. A crash or a concurrent read during the write
// therefore never sees a partial file - it sees either the previous
// snapshot or the new one, never a truncated one.
//
// Parent directories are created as needed. The final file is 0600: even
// after the redaction pass, the snapshot still carries resource identities,
// import IDs, and marker values, which is more than some users want
// world-readable on a shared build host (see stateless/RECEIPTS.md on what a
// scrub does and does not remove). Owner-only is the conservative default,
// and an operator who wants the file wider can chmod it afterwards.
// os.CreateTemp already creates the temp file at 0600, so the mode is right
// from the first byte and no chmod happens at any point.
//
// Before any of that, it refuses to write over a file that parses as an
// OpenTofu state file. The configuration decoder already rejects the paths
// that name one by convention (see internal/configs.validateSnapshotPath),
// but a name is not the file: a state file called anything at all is still
// the record of somebody's infrastructure, and a cache is never worth
// destroying one for. The refusal reaches the operator as the manager's
// snapshot warning, and the run is otherwise unaffected - which is the same
// contract every other snapshot write failure has.
func writeSnapshot(path string, snap *snapshot) error {
	if isStateFile(path) {
		return fmt.Errorf(
			"%s holds what looks like an OpenTofu state file, and the observational snapshot will not overwrite one; "+
				"point snapshot_path somewhere else", path,
		)
	}

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the observational snapshot: %w", err)
	}

	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	tmp, err := os.CreateTemp(dir, ".tofu-live-snapshot-*.tmp")
	if err != nil {
		return fmt.Errorf("creating a temp file for the observational snapshot: %w", err)
	}
	tmpPath := tmp.Name()
	// Cleaned up unless the rename below succeeds; renaming removes it from
	// under this path, so the second Remove is a harmless no-op.
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing the observational snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing the observational snapshot's temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming the observational snapshot into place: %w", err)
	}
	committed = true
	return nil
}

// isStateFile reports whether path names an existing file whose leading JSON
// object carries the keys an OpenTofu state file has: a "version" alongside
// at least one of the fields only a state file pairs with it.
//
// It reads tokens rather than the whole document, and stops at the first key
// pair that settles the question or after a bounded number of keys, so
// pointing snapshot_path at a large state file costs a few kilobytes of
// reading rather than parsing the whole record. It deliberately keys off
// "version" rather than "version": 4, so every state format version ever
// written - including ones stateless mode cannot read - is protected.
//
// A snapshot's own first key is "formatVersion", not "version", so this
// never refuses to overwrite the previous snapshot. Anything that is not a
// readable JSON object is not a state file as far as this is concerned; the
// ordinary write path deals with those.
func isStateFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	dec := json.NewDecoder(io.LimitReader(f, 64<<10))
	tok, err := dec.Token()
	if err != nil {
		return false
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return false
	}

	var haveVersion, haveStateKey bool
	for i := 0; i < 64; i++ {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		key, ok := tok.(string)
		if !ok {
			// The closing brace of an object with fewer keys than the bound.
			break
		}
		switch key {
		case "version":
			haveVersion = true
		case "terraform_version", "lineage", "serial", "resources":
			haveStateKey = true
		}
		if haveVersion && haveStateKey {
			return true
		}
		if err := skipJSONValue(dec); err != nil {
			break
		}
	}
	return haveVersion && haveStateKey
}

// skipJSONValue consumes one complete value from dec, descending through
// objects and arrays by counting delimiters, so that the caller's loop lands
// on the next key of the object it is walking.
func skipJSONValue(dec *json.Decoder) error {
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if delim, ok := tok.(json.Delim); ok {
			switch delim {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
		if depth <= 0 {
			return nil
		}
	}
}
