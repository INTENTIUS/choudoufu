// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// googleUncomposable says why a google type's composite record could not
// be composed into an import ID (INTENTIUS/choudoufu#2139 item 3), and
// whether typeName is a google type at all: only a google type's record
// is refused by name here, every other family's keeps the record leg's
// existing skip.
func googleUncomposable(typeName string) (why string, google bool) {
	if !strings.HasPrefix(typeName, "google_") {
		return "", false
	}
	if entry, rowed := identity.LookupType(typeName); rowed {
		if len(entry.Components) == 0 {
			return "Its identity-table row composes no import ID (the provider mints the identity)", true
		}
		return "Its identity-table row has a part the record does not carry", true
	}
	if reason, ok := identity.GoogleCompositeRefused[typeName]; ok {
		return "The type has no identity-table row and no import form tools/gcp-row-gen could read at hashicorp/google 8.6.0 (" + reason + ")", true
	}
	if _, ok := identity.GoogleCompositeImports[typeName]; ok {
		return "The record does not carry every part of any import form the provider states for the type", true
	}
	return "The type has no identity-table row and no import form tools/gcp-row-gen recorded for it", true
}

// googleIdentityImportWhy says why the record could not be imported by its
// identity instead (INTENTIUS/choudoufu#2145 item 4): the type's importer
// at the pin takes no identity, or the record lacks an attribute it needs.
func googleIdentityImportWhy(typeName string, components map[string]string) string {
	if reason, ok := identity.GoogleIdentityImportRefused[typeName]; ok {
		return "its importer takes no identity object at hashicorp/google 8.6.0 (" + reason + ")"
	}
	row, ok := identity.GoogleIdentityImports[typeName]
	if !ok {
		return "tools/gcp-row-gen recorded nothing about whether its importer takes an identity object"
	}
	var missing []string
	for _, name := range row.Required {
		if components[name] == "" {
			missing = append(missing, name)
		}
	}
	return "its importer takes an identity object only with " + strings.Join(missing, ", ") + ", which the record does not carry"
}

// componentNames lists a record's component attribute names, sorted.
func componentNames(components map[string]string) string {
	names := make([]string, 0, len(components))
	for n := range components {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "no attributes"
	}
	return strings.Join(names, ", ")
}
