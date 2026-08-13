// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Content pin for the CloudFormation Registry schema (issue #42), ported
// from chant's lexicons/aws/src/spec/pin.ts (chant #1390, #1473) - the
// semantics, not the code: chant is TypeScript with its own pinned-release
// store, this is a Go tool with a plain local cache.
//
// The registry bundle is a single "latest" artifact with no version in its
// path, republished constantly:
//
//	$ curl -sI https://schema.cloudformation.us-east-1.amazonaws.com/CloudformationSchema.zip
//	Last-Modified: Mon, 03 Aug 2026 01:29:20 GMT
//
// so there is no version to pin. The pin is over content instead - and
// specifically over the *extracted schemas*, not the zip: AWS repackaging
// the archive changes its bytes (and its ETag) while the schemas inside are
// identical, and a pin that fires on repackaging alone is noise. Sorted
// typeName -> sha256(schema), hashed in order, moves exactly when a schema
// does.
//
// Enforcement is softened the way chant #1473 learned to. chant originally
// refused on any digest mismatch, which made every build hostage to
// CloudFormation: the registry edits schemas and gains or loses types
// several times a day, and chant saw three distinct digests in one day with
// an *unchanged* type count. A pin that goes stale within hours cannot gate
// a build - it cannot tell "someone regenerated against a spec nobody
// chose" from "AWS edited a description this afternoon". So the two cases
// are separated here too:
//
//   - The resource set moved (a type added or removed): refuses. That
//     always changes what live/registry.json promises - a mapping or
//     synthesis batch consuming a type that silently disappeared, or
//     silently missing one that silently appeared, is exactly the
//     unreviewed-drift failure mode #1390 (chant) and #42 (here) exist to
//     prevent.
//   - Only bytes moved, with an identical type set: warns. Whether it
//     matters is a question for whoever reviews the regenerated
//     live/registry.json diff, not something a hash mismatch alone can
//     answer.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// SpecPin is the committed pin over the extracted CloudFormation Registry
// schemas.
type SpecPin struct {
	// Digest is "sha256:<hex>" over the sorted typeName -> schema content.
	Digest string `json:"digest"`
	// Resources is how many resource types the pinned archive contained.
	Resources int `json:"resources"`
	// Accepted is the ISO date the pin was accepted, so a bump reads as a
	// decision rather than one opaque hash replacing another.
	Accepted string `json:"accepted"`
}

// AcceptEnv is the env var that accepts whatever upstream currently serves:
// generation proceeds and warns instead of refusing, printing the pin block
// to paste into pinned_spec.go and pinned-types.json.
const AcceptEnv = "CHOUDOUFU_ACCEPT_REGISTRY_SPEC"

// ContentDigest hashes the sorted typeName -> sha256(schema) sequence.
// Stable against the archive being repackaged; moves when any schema's
// bytes change, or when a type is added or removed.
func ContentDigest(schemas map[string][]byte) string {
	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)

	h := sha256.New()
	for _, name := range names {
		h.Write([]byte(name))
		sum := sha256.Sum256(schemas[name])
		h.Write(sum[:])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// Drift is what moved between the pinned archive and one freshly extracted.
type Drift struct {
	Digest    string
	Resources int
	// Added and Removed are type names, only populated when pinnedNames was
	// supplied to ComputeDrift.
	Added   []string
	Removed []string
}

// ResourceSetMoved reports whether a type was added or removed - the
// condition that refuses under AssertPinned, as opposed to bytes-only
// drift, which only warns.
func (d *Drift) ResourceSetMoved() bool {
	return d != nil && (len(d.Added) > 0 || len(d.Removed) > 0)
}

// ComputeDrift compares freshly extracted schemas against pin. Returns nil
// when the digest matches. pinnedNames may be nil (no previous roster to
// diff against), in which case Added and Removed stay empty even though the
// digest moved - the caller can still see that the count and digest changed,
// just not which types did it.
func ComputeDrift(schemas map[string][]byte, pinnedNames map[string]bool, pin SpecPin) *Drift {
	digest := ContentDigest(schemas)
	if digest == pin.Digest {
		return nil
	}

	drift := &Drift{Digest: digest, Resources: len(schemas)}
	if pinnedNames == nil {
		return drift
	}
	for name := range schemas {
		if !pinnedNames[name] {
			drift.Added = append(drift.Added, name)
		}
	}
	for name := range pinnedNames {
		if _, ok := schemas[name]; !ok {
			drift.Removed = append(drift.Removed, name)
		}
	}
	sort.Strings(drift.Added)
	sort.Strings(drift.Removed)
	return drift
}

// DriftMessage renders the refusal (fatal) or the acceptance/advisory
// notice (not fatal) for a drift.
func DriftMessage(drift *Drift, pin SpecPin, fatal bool) string {
	delta := drift.Resources - pin.Resources
	countLine := fmt.Sprintf("%d resource types, unchanged in count", drift.Resources)
	if delta != 0 {
		sign := ""
		if delta > 0 {
			sign = "+"
		}
		countLine = fmt.Sprintf("%d resource types, %s%d against the pin", drift.Resources, sign, delta)
	}

	lines := []string{
		"The upstream CloudFormation Registry schema has moved since the pinned one.",
		"",
		fmt.Sprintf("  pinned    %s  (%d resources, accepted %s)", pin.Digest, pin.Resources, pin.Accepted),
		fmt.Sprintf("  upstream  %s  (%s)", drift.Digest, countLine),
	}
	if len(drift.Added) > 0 {
		lines = append(lines, "  added     "+sampleJoin(drift.Added))
	}
	if len(drift.Removed) > 0 {
		lines = append(lines, "  removed   "+sampleJoin(drift.Removed))
	}

	if fatal {
		lines = append(lines,
			"",
			"Generation refuses rather than regenerating against a spec nobody chose:",
			"a resource added or removed changes what live/registry.json promises, and",
			"a docs or codegen task on an unrelated branch would otherwise rewrite it",
			"with no commit saying why.",
		)
	} else {
		lines = append(lines,
			"",
			"The resource set is unchanged, so this is upstream editing schemas in",
			"place rather than a different spec. Generation continues; review the",
			"regenerated live/registry.json diff to see whether anything you rely on",
			"moved.",
		)
	}

	lines = append(lines,
		"",
		"To refresh the pin, confirm the delta is one you want and update",
		"tools/registry-gen/pinned_spec.go (and pinned-types.json, if the roster",
		"moved) in its own commit:",
		"",
		fmt.Sprintf("  Digest:    %q,", drift.Digest),
		fmt.Sprintf("  Resources: %d,", drift.Resources),
		"  Accepted:  \"<today>\",",
	)
	if fatal {
		lines = append(lines, "", fmt.Sprintf("Or re-run with %s=1 to proceed this once and print the same block.", AcceptEnv))
	}
	return strings.Join(lines, "\n")
}

// sampleJoin lists up to 5 names, then how many more there are.
func sampleJoin(names []string) string {
	const max = 5
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s (+%d more)", strings.Join(names[:max], ", "), len(names)-max)
}

// AssertPinned enforces the pin: nil when the extracted schemas match it
// (or moved in a way that's not fatal), an error naming the drift when they
// don't.
//
//   - No drift: returns nil, warn is not called.
//   - The resource set moved (a type added or removed) and accept is
//     false: returns a non-nil error carrying the fatal drift message.
//     Generation must not proceed on a resource set nobody reviewed.
//   - Byte-only drift (digest moved, type set identical): warns with the
//     advisory message and returns nil. Generation proceeds - AWS edits
//     schema bytes several times a day with the type count unchanged, and a
//     hard pin on that fires on nothing.
//   - accept is true: never returns an error. Warns with the same drift
//     detail (resource-set-moved or byte-only) so the accept-then-paste
//     loop is one command instead of two, and returns nil either way.
func AssertPinned(schemas map[string][]byte, pinnedNames map[string]bool, pin SpecPin, accept bool, warn func(string)) error {
	drift := ComputeDrift(schemas, pinnedNames, pin)
	if drift == nil {
		return nil
	}

	resourceSetMoved := drift.ResourceSetMoved()
	if accept {
		warn(DriftMessage(drift, pin, false))
		return nil
	}
	if resourceSetMoved {
		return fmt.Errorf("%s", DriftMessage(drift, pin, true))
	}
	warn(DriftMessage(drift, pin, false))
	return nil
}
