// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"
	"strings"
)

// This file is the exported face of helpers the rest of this package keeps
// unexported. Each one is what a caller outside the package - choudoufu's
// configuration-reading resolver in internal/live/identity, or plunge -
// needs to apply a row the same way this package does. They are thin
// wrappers so the rules themselves stay in one place.

// Describe renders a cloud property for an operator-facing sentence.
func (c CloudValue) Describe() string { return c.describe() }

// Describe is what the provider does to a value this rule names, as a
// sentence for a refusal.
func (l LongFormRule) Describe() string { return l.describe() }

// Value returns the context's value for one cloud property, and whether the
// caller supplied it. An empty value is reported as not supplied.
func (c CloudContext) Value(which CloudValue) (string, bool) { return c.value(which) }

// IdentityAttrFor is the identity attribute this component supplies for one
// instance, given which of its alternative arguments (argName) the
// configuration actually set. The empty string means it supplies none.
func (c Component) IdentityAttrFor(argName string) string { return c.identityAttrFor(argName) }

// HasIdentityAttr reports whether name is one of the entry's
// [TypeIdentity.IdentityAttrs].
func (t TypeIdentity) HasIdentityAttr(name string) bool { return t.hasIdentityAttr(name) }

// PreferSynthesized decides between a hand-written row and the entry the
// provider's own identity schema synthesizes for the same type. When the
// synthesized entry reproduces the row it returns the entry to use in the
// row's place and true; otherwise it returns false and the row stands.
func PreferSynthesized(row, synthesized TypeIdentity) (TypeIdentity, bool) {
	return preferSynthesized(row, synthesized)
}

// CanonicalAzureEnvironment is the lower-case name of an Azure environment
// this package knows DNS suffixes for, and false for one it does not. Both
// providers match the name without regard to case.
func CanonicalAzureEnvironment(name string) (string, bool) { return canonicalAzureEnvironment(name) }

// AzureSuffixService reports whether which is one of the Azure DNS suffix
// cloud values ([CloudKeyVaultDNSSuffix] and its siblings), returning it
// unchanged when it is.
func AzureSuffixService(which CloudValue) (CloudValue, bool) { return azureSuffixService(which) }

// NamedAttrs is every attribute name this entry mentions, on either side:
// the arguments its components read and the attributes it hands out as
// identity sources. It is the set an identity schema's attribute has to
// land in for the entry to be talking about the same identity.
func (t TypeIdentity) NamedAttrs() map[string]bool {
	out := make(map[string]bool)
	for _, c := range t.Components {
		for _, a := range c.Attrs {
			out[a] = true
		}
	}
	for _, a := range t.IdentityAttrs {
		out[a] = true
	}
	return out
}

// Claim renders what this entry says identifies the type, for a message
// that has to show both sides.
func (t TypeIdentity) Claim() string {
	if t.ServerAssigned {
		if len(t.IdentityAttrs) > 0 {
			return fmt.Sprintf("a server-assigned %s", strings.Join(t.IdentityAttrs, "/"))
		}
		return "a server-assigned identity"
	}
	parts := make([]string, 0, len(t.Components))
	for _, c := range t.Components {
		switch {
		case c.Cloud != CloudNone:
			parts = append(parts, "the run's "+c.Cloud.describe())
		case len(c.Attrs) == 0:
			parts = append(parts, fmt.Sprintf("%q", c.Literal))
		default:
			parts = append(parts, strings.Join(c.Attrs, "|"))
		}
	}
	return strings.Join(parts, " + ")
}

// Attr is the component a row writes for an argument that names the
// instance under its own name: it reads the first of names the
// configuration sets, and supplies the identity attribute of that same name.
func Attr(names ...string) Component { return attr(names...) }

// InAttr is c, supplying the named identity attribute rather than the one
// its own argument name implies. Several components wrapped in the same name
// concatenate into it, in order.
func InAttr(identityAttr string, c Component) Component { return inAttr(identityAttr, c) }

// SchemaReproducesRow reports whether an entry synthesized from the
// provider's identity schema builds the same identity a hand-written row
// does. [PreferSynthesized] is the call that decides which one to use.
func SchemaReproducesRow(row, synthesized TypeIdentity) bool {
	return schemaReproducesRow(row, synthesized)
}
