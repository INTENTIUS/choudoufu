// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Overlay is the curated file at tools/mapping-gen/overlay.json: the hand
// asserted facts the name heuristic in heuristic.go cannot derive on its
// own. Six tables. Five - Aliases, Folds, Nones, TFOnly, CFNUnmodeled - are
// disjoint and keyed by TF type; the sixth - ServiceAliases - is keyed by TF
// type *prefix* (a different domain, so it cannot collide with the other
// five) and drives the service-scoped heuristic in servicealias.go:
//
//   - Aliases pairs a TF type straight to its CFN type when the two names
//     just do not correspond - a legacy TF name (aws_cloudwatch_event_rule),
//     an abbreviated service (RDS/db), a dropped EC2 prefix (aws_vpc, not
//     aws_ec2_vpc), and so on.
//   - Folds names a TF type that is a property-child of a CFN parent:
//     Terraform decomposes some resources finer than CloudFormation does (an
//     S3 bucket's versioning, encryption and lifecycle sub-resources are all
//     just properties on CloudFormation's one AWS::S3::Bucket), so the child
//     carries no CFN type of its own - the map's value is the parent's CFN
//     type, which becomes the row's fold_parent.
//   - Nones names a TF type with no CFN counterpart at all, and no further
//     taxonomy has been curated for it yet - a bare via:none judgment, the
//     one the terminal taxonomy (issue #53) is meant to empty out. Prefer
//     TFOnly or CFNUnmodeled below for anything with a known reason.
//   - TFOnly names a TF type that is a provider-side construct with no cloud
//     resource of its own - a waiter, a validation, an aws_ami_copy-style
//     operation - and says what it is instead. via:tf-only. Most tf-only
//     rows in live/mapping.json instead come from mapping.go's own
//     pattern+corroboration mechanical classifier, run after every source in
//     this file; this table is for a family sweep's hand judgment when that
//     classifier's evidence bar (a name pattern plus no importable identity
//     in the provider's schema) cannot reach a type on its own.
//   - CFNUnmodeled names a TF type that is a real, live AWS resource the
//     CloudFormation Registry simply does not model (e.g. aws_s3_object),
//     and says what the service-scoped registry search found (or didn't).
//     via:cfn-unmodeled. Curated only - proving a resource has no CFN model
//     at all is a per-family judgment call, not something this tool derives
//     mechanically.
//   - ServiceAliases pairs a TF type prefix (e.g. "vpc", "cloudwatch_log")
//     with the CFN service name(s) it corresponds to, for prefixes whose
//     relationship to a CFN service the name heuristic's own service-casing
//     rule (heuristic.go's nameCandidates) cannot derive - a TF prefix that
//     names no real AWS service at all (vpc -> EC2), or a compound CFN
//     service name a shorter TF prefix does not spell out (route53_resolver
//     -> Route53Resolver). One entry converts a whole family at once; see
//     servicealias.go for how a prefix, once matched, is turned into a CFN
//     type match scoped to that service alone.
//
// A TF type appearing in more than one of the first five tables is almost
// certainly a curation mistake (the loader below refuses it), and every
// entry in every table must name a type (or, for ServiceAliases, a service)
// that still exists in the current TF and CFN rosters - see the two-way
// staleness tests in mapping_gen_test.go, which also refuse an alias the
// name heuristic has since grown to cover on its own.
type Overlay struct {
	// Aliases maps tf_type -> cfn_type.
	Aliases map[string]string `json:"aliases"`

	// Folds maps tf_type -> the parent's cfn_type (the row's fold_parent).
	Folds map[string]string `json:"folds"`

	// Nones maps tf_type -> the reason it has no CFN counterpart, with no
	// further taxonomy curated yet.
	Nones map[string]string `json:"nones"`

	// TFOnly maps tf_type -> what the type is instead of a CFN resource
	// (issue #53's via:tf-only).
	TFOnly map[string]string `json:"tf_only"`

	// CFNUnmodeled maps tf_type -> the registry-search evidence that no CFN
	// type models it (issue #53's via:cfn-unmodeled).
	CFNUnmodeled map[string]string `json:"cfn_unmodeled"`

	// ServiceAliases maps a TF type prefix (no "aws_" prefix, no trailing
	// underscore, e.g. "vpc" or "cloudwatch_log") to the CFN service
	// name(s) - as they appear in the registry's AWS::<Service>::<Resource>
	// type names - that prefix's TF types belong to.
	ServiceAliases map[string][]string `json:"service_aliases"`
}

// loadOverlay reads and validates tools/mapping-gen/overlay.json, merged
// with any fragment files under tools/mapping-gen/overlay.d/*.json beside
// it. Fragments carry the same schema and exist so that family sweeps
// (issue #53) can land in parallel without contending for one file; a key
// defined twice anywhere across the base and the fragments is refused, so
// a fragment can only ever add.
func loadOverlay(path string) (Overlay, error) {
	ov, err := decodeOverlay(path)
	if err != nil {
		return Overlay{}, err
	}

	fragDir := filepath.Join(filepath.Dir(path), "overlay.d")
	entries, err := os.ReadDir(fragDir)
	if err != nil && !os.IsNotExist(err) {
		return Overlay{}, fmt.Errorf("reading %s: %w", fragDir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		fragPath := filepath.Join(fragDir, name)
		frag, err := decodeOverlay(fragPath)
		if err != nil {
			return Overlay{}, err
		}
		if err := mergeOverlay(&ov, frag, fragPath); err != nil {
			return Overlay{}, err
		}
	}

	return validateOverlay(ov, path)
}

// decodeOverlay reads one overlay-schema file without validating it; the
// merged result is validated once, in loadOverlay.
func decodeOverlay(path string) (Overlay, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return Overlay{}, err
	}
	var ov Overlay
	if err := json.Unmarshal(data, &ov); err != nil {
		return Overlay{}, fmt.Errorf("decoding %s: %w", path, err)
	}
	return ov, nil
}

// mergeOverlay adds frag's entries to base, refusing any key either side
// already holds - across fragments a type or prefix has exactly one home.
func mergeOverlay(base *Overlay, frag Overlay, fragPath string) error {
	merge := func(dst *map[string]string, src map[string]string, table string) error {
		if len(src) == 0 {
			return nil
		}
		if *dst == nil {
			*dst = map[string]string{}
		}
		for k, v := range src {
			if _, ok := (*dst)[k]; ok {
				return fmt.Errorf("%s: %s entry %q is already defined by the base overlay or an earlier fragment", fragPath, table, k)
			}
			(*dst)[k] = v
		}
		return nil
	}
	if err := merge(&base.Aliases, frag.Aliases, "aliases"); err != nil {
		return err
	}
	if err := merge(&base.Folds, frag.Folds, "folds"); err != nil {
		return err
	}
	if err := merge(&base.Nones, frag.Nones, "nones"); err != nil {
		return err
	}
	if err := merge(&base.TFOnly, frag.TFOnly, "tf_only"); err != nil {
		return err
	}
	if err := merge(&base.CFNUnmodeled, frag.CFNUnmodeled, "cfn_unmodeled"); err != nil {
		return err
	}
	for prefix, services := range frag.ServiceAliases {
		if _, ok := base.ServiceAliases[prefix]; ok {
			return fmt.Errorf("%s: service_aliases prefix %q is already defined by the base overlay or an earlier fragment", fragPath, prefix)
		}
		if base.ServiceAliases == nil {
			base.ServiceAliases = map[string][]string{}
		}
		base.ServiceAliases[prefix] = services
	}
	return nil
}

// validateOverlay holds the merged overlay to the invariants the table
// doc comment above states.
func validateOverlay(ov Overlay, path string) (Overlay, error) {

	seen := map[string]string{}
	for _, table := range []struct {
		name string
		keys map[string]string
	}{
		{"aliases", ov.Aliases},
		{"folds", ov.Folds},
		{"nones", ov.Nones},
		{"tf_only", ov.TFOnly},
		{"cfn_unmodeled", ov.CFNUnmodeled},
	} {
		for tfType, value := range table.keys {
			if value == "" {
				return Overlay{}, fmt.Errorf("%s: overlay %s entry for %s has an empty value", path, table.name, tfType)
			}
			if prior, ok := seen[tfType]; ok {
				return Overlay{}, fmt.Errorf("%s: %s appears in both %s and %s - a TF type belongs in exactly one overlay table",
					path, tfType, prior, table.name)
			}
			seen[tfType] = table.name
		}
	}

	for prefix, services := range ov.ServiceAliases {
		if prefix == "" {
			return Overlay{}, fmt.Errorf("%s: service_aliases has an empty-string prefix key", path)
		}
		if strings.HasPrefix(prefix, "aws_") || strings.HasPrefix(prefix, "_") || strings.HasSuffix(prefix, "_") {
			return Overlay{}, fmt.Errorf("%s: service_aliases prefix %q must be bare (no leading \"aws_\", no leading or trailing underscore)", path, prefix)
		}
		if len(services) == 0 {
			return Overlay{}, fmt.Errorf("%s: service_aliases entry for %q names no CFN service", path, prefix)
		}
		for _, svc := range services {
			if svc == "" {
				return Overlay{}, fmt.Errorf("%s: service_aliases entry for %q has an empty service name", path, prefix)
			}
		}
	}
	return ov, nil
}
