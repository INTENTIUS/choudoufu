// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/strict"
)

// This file is GitHub issue #1515's envelope split: which parts of a
// [recordEnvelope] are secret, how they are taken out and replaced by a
// [secretRef], and how they are put back. It knows nothing about SSM; the
// store that writes and reads the values is ssmsecrets.go.
//
// A record carries secret material in three places, and those are the only
// places this moves anything out of:
//
//   - Object.Attrs at the paths Object.SensitiveAttrs names, for a
//     record-backed resource (random_password, tls_private_key): the record
//     is the only copy of the value. The stored Attrs holds a JSON null at
//     each such path.
//   - Object.Private, and Residue.ProviderPrivate, the provider's own
//     private bytes. Omitted from the stored record.
//   - Residue.Attributes at the keys Residue.Sensitive names: a value last
//     sent for an argument the API never returns, such as a database's
//     master password. The stored attribute keeps its type and holds a null.
//
// Everything else - identity, value types, the sensitive-path list itself,
// non-sensitive residue, deposed and tombstone entries - stays in the
// record, so a plan still reads identity from one store.
//
// # Byte identity
//
// What a reader above the store gets back must be the bytes the writer
// handed it, exactly: [RecordStore.mergeEnvelope] compares a planned write
// against the payload it read (#1938), and a payload that came back
// re-encoded would read as a change on every run. So the split works on
// byte spans of the encoded value rather than re-encoding it, and
// [SSMSecretsStore] checks every split by restoring it and comparing before
// anything is written. A split that does not restore to the same bytes is
// refused rather than stored.

// Where a [secretRef]'s value came from.
const (
	secretInObjectAttrs     = "object.attrs"
	secretInObjectPrivate   = "object.private"
	secretInResidueAttr     = "residue.attributes"
	secretInResiduePrivate  = "residue.provider_private"
	secretTierAdvancedLabel = "Advanced"
)

// secretRef is one entry of [recordEnvelope.SecretRefs]: one SecureString
// parameter and the place in the envelope its value belongs.
type secretRef struct {
	// In is one of the secretIn* constants.
	In string `json:"in"`

	// Path is the place inside Object.Attrs, for In == object.attrs, in the
	// state file's own step encoding (sensitivepaths.go's [pathStepJSON]).
	// It is the path that was actually replaced, which is the sensitive
	// path itself or, when a step cannot be followed in the encoded value
	// (an element of a set), the deepest ancestor that can.
	Path json.RawMessage `json:"path,omitempty"`

	// Attribute is the Residue.Attributes key, for In == residue.attributes.
	Attribute string `json:"attribute,omitempty"`

	// Parameter is the parameter's full name.
	Parameter string `json:"parameter"`

	// Tier is "Advanced" for a value over the standard tier's 4 KB, and
	// absent otherwise. It is in the record so that a run which only reads
	// can still announce what the estate is paying for (ruling 3).
	Tier string `json:"tier,omitempty"`
}

// pendingSecret is one secret value [splitSecrets] took out of an envelope,
// with the reference that will stand in for it once a parameter name is
// assigned.
type pendingSecret struct {
	ref   secretRef
	value string
}

// slot is what identifies a pendingSecret's place independently of the
// parameter it ends up in: two writes of one record that move the same
// value out of the same place may share a parameter. See
// [SSMSecretsStore.assignParameters].
func (r secretRef) slot() string {
	return r.In + "\x00" + string(r.Path) + "\x00" + r.Attribute
}

// UnresolvedSecretRefsError is what [decodeEnvelope] returns for a record
// that still carries references: the record was written under
// `strict { secrets = "ssm" }` and this run opened the store without it.
type UnresolvedSecretRefsError struct {
	Parameters []string
}

func (e *UnresolvedSecretRefsError) Error() string {
	return fmt.Sprintf("the stored record keeps %d secret value(s) in SSM Parameter Store (%s) and this run did not open the record store with strict { secrets = \"ssm\" }, so it cannot read them; refusing rather than reading each one as null (GitHub issue #1515)",
		len(e.Parameters), strings.Join(e.Parameters, ", "))
}

func secretRefParameters(refs []secretRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Parameter)
	}
	return out
}

// errSecretSplitNotIdentity is the self-check's refusal: the split would not
// give back the bytes it was handed. See this file's "Byte identity".
var errSecretSplitNotIdentity = errors.New("the record's secret values could not be separated from it in a way that restores the same bytes, so it was not written; this is a defect in choudoufu (GitHub issue #1515), not in the configuration")

// splitSecrets takes env's secret values out and returns them. env is
// modified in place: each value's place is cleared as this file's header
// describes, and env.SecretRefs is left unset for the caller, which still
// has to name the parameters. A record with no secret value returns none
// and leaves env alone.
func splitSecrets(env *recordEnvelope) ([]pendingSecret, error) {
	var out []pendingSecret
	if of := env.Object; of != nil {
		if len(of.SensitiveAttrs) > 0 && len(of.Attrs) > 0 {
			moved, attrs, err := splitObjectAttrs(of.Attrs, of.SensitiveAttrs)
			if err != nil {
				return nil, err
			}
			if len(moved) > 0 {
				of.Attrs = attrs
				out = append(out, moved...)
			}
		}
		if len(of.Private) > 0 {
			out = append(out, pendingSecret{
				ref:   secretRef{In: secretInObjectPrivate},
				value: base64.StdEncoding.EncodeToString(of.Private),
			})
			of.Private = nil
		}
	}
	if rf := env.Residue; rf != nil {
		for _, name := range rf.Sensitive {
			av, ok := rf.Attributes[name]
			if !ok || isJSONNull(av.Value) {
				continue
			}
			out = append(out, pendingSecret{
				ref:   secretRef{In: secretInResidueAttr, Attribute: name},
				value: string(av.Value),
			})
			av.Value = json.RawMessage("null")
			rf.Attributes[name] = av
		}
		if len(rf.ProviderPrivate) > 0 {
			out = append(out, pendingSecret{
				ref:   secretRef{In: secretInResiduePrivate},
				value: base64.StdEncoding.EncodeToString(rf.ProviderPrivate),
			})
			rf.ProviderPrivate = nil
		}
	}
	return out, nil
}

// restoreSecrets is [splitSecrets] backwards: it puts each referenced value
// back where its reference says it came from and drops env.SecretRefs.
// values maps a parameter name to its value; a reference whose parameter
// is not there is an error, never a null.
func restoreSecrets(env *recordEnvelope, values map[string]string) error {
	refs := env.SecretRefs
	env.SecretRefs = nil
	for _, ref := range refs {
		v, ok := values[ref.Parameter]
		if !ok {
			return &SecretParameterMissingError{Parameter: ref.Parameter}
		}
		switch ref.In {
		case secretInObjectAttrs:
			if env.Object == nil {
				return fmt.Errorf("the record references %s for its object's attributes and carries no object", ref.Parameter)
			}
			var steps []pathStepJSON
			if err := json.Unmarshal(ref.Path, &steps); err != nil {
				return fmt.Errorf("the reference to %s has an unreadable path: %w", ref.Parameter, err)
			}
			attrs, err := spliceAtPath(env.Object.Attrs, steps, []byte(v))
			if err != nil {
				return fmt.Errorf("putting %s back into the record: %w", ref.Parameter, err)
			}
			env.Object.Attrs = attrs
		case secretInObjectPrivate:
			if env.Object == nil {
				return fmt.Errorf("the record references %s for its object's private data and carries no object", ref.Parameter)
			}
			b, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				return fmt.Errorf("the value of %s is not the base64 this store writes: %w", ref.Parameter, err)
			}
			env.Object.Private = b
		case secretInResidueAttr:
			if env.Residue == nil {
				return fmt.Errorf("the record references %s for a residue attribute and carries no residue", ref.Parameter)
			}
			av, ok := env.Residue.Attributes[ref.Attribute]
			if !ok || !isJSONNull(av.Value) {
				return fmt.Errorf("the record references %s for residue attribute %q, which is not a null placeholder in the record", ref.Parameter, ref.Attribute)
			}
			av.Value = json.RawMessage(v)
			env.Residue.Attributes[ref.Attribute] = av
		case secretInResiduePrivate:
			if env.Residue == nil {
				return fmt.Errorf("the record references %s for its residue's provider private data and carries no residue", ref.Parameter)
			}
			b, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				return fmt.Errorf("the value of %s is not the base64 this store writes: %w", ref.Parameter, err)
			}
			env.Residue.ProviderPrivate = b
		default:
			return fmt.Errorf("the record references %s at %q, a place this version of choudoufu does not know", ref.Parameter, ref.In)
		}
	}
	return nil
}

// splitObjectAttrs replaces the value at each sensitive path in attrs with
// null and returns what it replaced.
func splitObjectAttrs(attrs, sensitiveRaw json.RawMessage) ([]pendingSecret, json.RawMessage, error) {
	var paths [][]pathStepJSON
	if err := json.Unmarshal(sensitiveRaw, &paths); err != nil {
		return nil, nil, fmt.Errorf("reading the record's sensitive paths: %w", err)
	}
	type found struct {
		start, end int
		steps      []pathStepJSON
	}
	var spans []found
	for _, path := range paths {
		start, end, depth, ok, err := locatePath(attrs, path)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}
		spans = append(spans, found{start: start, end: end, steps: path[:depth]})
	}
	// An ancestor covers its descendants, and two paths that fall back to
	// one ancestor are one value.
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	var kept []found
	for _, s := range spans {
		if n := len(kept); n > 0 && s.start >= kept[n-1].start && s.end <= kept[n-1].end {
			continue
		}
		kept = append(kept, s)
	}
	var out []pendingSecret
	buf := []byte(attrs)
	// Back to front, so each earlier span's offsets still hold.
	for i := len(kept) - 1; i >= 0; i-- {
		s := kept[i]
		value := buf[s.start:s.end]
		if isJSONNull(value) {
			continue
		}
		pathRaw, err := json.Marshal(s.steps)
		if err != nil {
			return nil, nil, fmt.Errorf("encoding a sensitive path: %w", err)
		}
		out = append(out, pendingSecret{
			ref:   secretRef{In: secretInObjectAttrs, Path: pathRaw},
			value: string(value),
		})
		next := make([]byte, 0, len(buf)-(s.end-s.start)+4)
		next = append(next, buf[:s.start]...)
		next = append(next, "null"...)
		next = append(next, buf[s.end:]...)
		buf = next
	}
	// Front to back, for a stable order in the record.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, json.RawMessage(buf), nil
}

// spliceAtPath replaces the null at steps in doc with value.
func spliceAtPath(doc json.RawMessage, steps []pathStepJSON, value []byte) (json.RawMessage, error) {
	start, end, depth, ok, err := locatePath(doc, steps)
	if err != nil {
		return nil, err
	}
	if !ok || depth != len(steps) {
		return nil, fmt.Errorf("the path it was taken from is not in the record's attributes")
	}
	if !isJSONNull(doc[start:end]) {
		return nil, fmt.Errorf("the record's attributes hold a value, not the null placeholder, where it belongs")
	}
	out := make([]byte, 0, len(doc)+len(value))
	out = append(out, doc[:start]...)
	out = append(out, value...)
	out = append(out, doc[end:]...)
	return json.RawMessage(out), nil
}

// locatePath finds the byte span in doc of the value at steps. It follows
// as many steps as the encoded value allows and returns how many it
// followed: a step into a set element, whose JSON is an array with no key
// to match, stops there and the span is the set's. ok is false when a step
// names something that is not there at all (an attribute the value does
// not have, an index past the end of a list), which means there is no
// value at that path to move.
func locatePath(doc []byte, steps []pathStepJSON) (start, end, depth int, ok bool, err error) {
	start = skipJSONSpace(doc, 0)
	end, err = scanJSONValue(doc, start)
	if err != nil {
		return 0, 0, 0, false, err
	}
	for depth < len(steps) {
		step := steps[depth]
		var (
			objectKey string
			arrayIdx  = -1
			byKey     bool
		)
		switch step.Type {
		case getAttrPathStepType:
			if err := json.Unmarshal(step.Value, &objectKey); err != nil {
				return 0, 0, 0, false, fmt.Errorf("reading a sensitive path's attribute step: %w", err)
			}
			byKey = true
		case indexPathStepType:
			var key struct {
				Value json.RawMessage `json:"value"`
				Type  json.RawMessage `json:"type"`
			}
			if err := json.Unmarshal(step.Value, &key); err != nil {
				return 0, 0, 0, false, fmt.Errorf("reading a sensitive path's index step: %w", err)
			}
			switch string(key.Type) {
			case `"string"`:
				if err := json.Unmarshal(key.Value, &objectKey); err != nil {
					return 0, 0, 0, false, fmt.Errorf("reading a sensitive path's index key: %w", err)
				}
				byKey = true
			case `"number"`:
				n, convErr := strconv.Atoi(string(key.Value))
				if convErr != nil || n < 0 {
					return start, end, depth, true, nil
				}
				arrayIdx = n
			default:
				// A set element's key is the element itself. Its JSON is
				// an array position nothing in the key names, so the set
				// as a whole is the value.
				return start, end, depth, true, nil
			}
		default:
			return 0, 0, 0, false, fmt.Errorf("a sensitive path has a %q step, which this store cannot follow", step.Type)
		}
		if start >= len(doc) {
			return 0, 0, 0, false, nil
		}
		switch {
		case byKey && doc[start] == '{':
			s, e, found, err := jsonObjectMember(doc, start, objectKey)
			if err != nil {
				return 0, 0, 0, false, err
			}
			if !found {
				return 0, 0, 0, false, nil
			}
			start, end = s, e
		case arrayIdx >= 0 && doc[start] == '[':
			s, e, found, err := jsonArrayElement(doc, start, arrayIdx)
			if err != nil {
				return 0, 0, 0, false, err
			}
			if !found {
				return 0, 0, 0, false, nil
			}
			start, end = s, e
		case isJSONNull(doc[start:end]):
			// A null ancestor holds nothing below it.
			return 0, 0, 0, false, nil
		default:
			// The step does not fit the value's shape (a number index into
			// an object, a key into an array): the value here is as deep as
			// the encoding goes.
			return start, end, depth, true, nil
		}
		depth++
	}
	return start, end, depth, true, nil
}

func isJSONNull(b []byte) bool {
	return bytes.Equal(bytes.TrimSpace(b), []byte("null"))
}

func skipJSONSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// scanJSONValue returns the offset just past the JSON value starting at i.
func scanJSONValue(b []byte, i int) (int, error) {
	if i >= len(b) {
		return 0, errors.New("unexpected end of the record's attributes")
	}
	switch b[i] {
	case '"':
		return scanJSONString(b, i)
	case '{', '[':
		closer := byte('}')
		if b[i] == '[' {
			closer = ']'
		}
		j := skipJSONSpace(b, i+1)
		if j < len(b) && b[j] == closer {
			return j + 1, nil
		}
		for {
			if b[i] == '{' {
				var err error
				j, err = scanJSONString(b, j)
				if err != nil {
					return 0, err
				}
				j = skipJSONSpace(b, j)
				if j >= len(b) || b[j] != ':' {
					return 0, errors.New("malformed object in the record's attributes")
				}
				j = skipJSONSpace(b, j+1)
			}
			var err error
			j, err = scanJSONValue(b, j)
			if err != nil {
				return 0, err
			}
			j = skipJSONSpace(b, j)
			if j >= len(b) {
				return 0, errors.New("unexpected end of the record's attributes")
			}
			if b[j] == closer {
				return j + 1, nil
			}
			if b[j] != ',' {
				return 0, errors.New("malformed container in the record's attributes")
			}
			j = skipJSONSpace(b, j+1)
		}
	default:
		j := i
		for j < len(b) && !strings.ContainsRune(",}] \t\r\n", rune(b[j])) {
			j++
		}
		if j == i {
			return 0, errors.New("malformed value in the record's attributes")
		}
		return j, nil
	}
}

func scanJSONString(b []byte, i int) (int, error) {
	if i >= len(b) || b[i] != '"' {
		return 0, errors.New("expected a string in the record's attributes")
	}
	for j := i + 1; j < len(b); j++ {
		switch b[j] {
		case '\\':
			j++
		case '"':
			return j + 1, nil
		}
	}
	return 0, errors.New("unterminated string in the record's attributes")
}

// jsonObjectMember finds key's value span in the object starting at i.
func jsonObjectMember(b []byte, i int, key string) (start, end int, found bool, err error) {
	j := skipJSONSpace(b, i+1)
	if j < len(b) && b[j] == '}' {
		return 0, 0, false, nil
	}
	for j < len(b) {
		ks := j
		ke, err := scanJSONString(b, ks)
		if err != nil {
			return 0, 0, false, err
		}
		var name string
		if err := json.Unmarshal(b[ks:ke], &name); err != nil {
			return 0, 0, false, err
		}
		j = skipJSONSpace(b, ke)
		if j >= len(b) || b[j] != ':' {
			return 0, 0, false, errors.New("malformed object in the record's attributes")
		}
		vs := skipJSONSpace(b, j+1)
		ve, err := scanJSONValue(b, vs)
		if err != nil {
			return 0, 0, false, err
		}
		if name == key {
			return vs, ve, true, nil
		}
		j = skipJSONSpace(b, ve)
		if j < len(b) && b[j] == ',' {
			j = skipJSONSpace(b, j+1)
			continue
		}
		break
	}
	return 0, 0, false, nil
}

// jsonArrayElement finds element idx's span in the array starting at i.
func jsonArrayElement(b []byte, i, idx int) (start, end int, found bool, err error) {
	j := skipJSONSpace(b, i+1)
	if j < len(b) && b[j] == ']' {
		return 0, 0, false, nil
	}
	for n := 0; j < len(b); n++ {
		ve, err := scanJSONValue(b, j)
		if err != nil {
			return 0, 0, false, err
		}
		if n == idx {
			return j, ve, true, nil
		}
		j = skipJSONSpace(b, ve)
		if j < len(b) && b[j] == ',' {
			j = skipJSONSpace(b, j+1)
			continue
		}
		break
	}
	return 0, 0, false, nil
}

// markSensitiveResidue fills rf.Sensitive under `strict { secrets = "ssm" }`
// and does nothing under any other setting, so a record written under
// "store" or "refuse" is the same bytes it always was. A key is sensitive
// when the schema says so: a flat attribute by its own flag, a path key by
// the attribute it leads to, a whole nested block when anything inside it
// is. A path key that cannot be read is counted sensitive, since moving a
// value that need not move costs a parameter and leaving one that should
// costs the secret.
func markSensitiveResidue(rf *residueFields, block *configschema.Block, secrets strict.Secrets) {
	if rf == nil || !strict.SecretsInSSM(secrets) {
		return
	}
	var names []string
	for key := range rf.Attributes {
		if residueKeySensitive(block, key) {
			names = append(names, key)
		}
	}
	sort.Strings(names)
	rf.Sensitive = names
}

func residueKeySensitive(block *configschema.Block, key string) bool {
	if block == nil {
		return true
	}
	if isResiduePathKey(key) {
		path, err := decodeResiduePathKey(key)
		if err != nil {
			return true
		}
		attr := block.AttributeByPath(path)
		return attr == nil || attr.Sensitive
	}
	if a := block.Attributes[key]; a != nil {
		return a.Sensitive
	}
	if nb := block.BlockTypes[key]; nb != nil {
		return nb.Block.ContainsSensitive()
	}
	return true
}
