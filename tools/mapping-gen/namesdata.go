// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package main (namesdata.go): the first of issue #52's two authoritative
// sources for the service-alias table that overlay.json's service_aliases
// section used to hand-curate.
//
// hashicorp/terraform-provider-aws ships names/data/names_data.hcl: the
// provider's own reference data for every AWS service it wires, one
// `service "prefix" { ... }` block per family, each carrying (among other
// things) the AWS SDK's own service identifier (sdk.id, e.g. "EC2") and,
// for a family the provider itself further splits (ec2's nine sub_service
// blocks - vpc, ipam, transitgateway, ...), a nested `sub_service "prefix"
// { ... }` block per split.
//
// The join this file computes is exactly the one issue #52 names: TF
// prefix -> AWS service id -> CFN service name. A block's own name is
// always its resource prefix (resource_prefix.correct is, in every
// instance checked against the pinned v6.58.0 file, precisely "aws_" plus
// the block's own label plus "_" - the field carries no information the
// label itself does not already carry, so the label is used directly and
// the correct field is not parsed at all).
//
// Every one of the file's 9 sub_service blocks (all under "ec2") carries an
// empty sdk.id of its own - the file's own signal that the split is a TF
// naming convenience, not a distinct AWS API surface, so a sub_service's
// effective AWS id is always its parent's sdk.id (or the parent's
// provider_name_upper when even that is empty), never anything of the
// sub_service block's own. vpc -> EC2 falls out of this rule directly: vpc
// is a sub_service of ec2, so its effective id is ec2's own "EC2".
//
// The AWS id (sdk.id when non-empty, else names.provider_name_upper) is
// matched against live/registry.json's CFN service names case- and
// separator-insensitively (sdk.id often carries spaces the CFN name never
// does, e.g. "Application Auto Scaling" -> ApplicationAutoScaling): a
// unique match generates a service_aliases-shaped entry, prefix -> that CFN
// service; no match, or more than one CFN service sharing the same
// normalized spelling (never observed against the pinned registry, but
// checked rather than assumed), is a genuine wrinkle - most of them
// services CloudFormation simply does not model as their own resource
// family, e.g. "cognitoidp" (Cognito's IDP-only SDK client, folded under
// CloudFormation's own "Cognito" service, itself reached through the
// "cognito" block, not this one) - counted and reported rather than
// silently dropped.
package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
)

// The HCL shapes below decode names_data.hcl's service/sub_service blocks.
// Every block carries fields this join has no use for (cli_v2_command,
// go_packages, env_var, endpoint_info, brand, ...) - each nested block type
// declares its own `Remain hcl.Body` so gohcl does not refuse the file for
// attributes or blocks it was never asked to decode.

type nsSDKBlock struct {
	ID     string   `hcl:"id,optional"`
	Remain hcl.Body `hcl:",remain"`
}

type nsNamesBlock struct {
	ProviderNameUpper string   `hcl:"provider_name_upper,optional"`
	Remain            hcl.Body `hcl:",remain"`
}

type nsSubServiceBlock struct {
	Name   string        `hcl:"name,label"`
	SDK    *nsSDKBlock   `hcl:"sdk,block"`
	Names  *nsNamesBlock `hcl:"names,block"`
	Remain hcl.Body      `hcl:",remain"`
}

type nsServiceBlock struct {
	Name        string              `hcl:"name,label"`
	SDK         *nsSDKBlock         `hcl:"sdk,block"`
	Names       *nsNamesBlock       `hcl:"names,block"`
	SubServices []nsSubServiceBlock `hcl:"sub_service,block"`
	Remain      hcl.Body            `hcl:",remain"`
}

type namesDataFile struct {
	Services []nsServiceBlock `hcl:"service,block"`
}

// namesDataService is one flattened family this join can produce a
// service_aliases entry for: a top-level service block, or one of its
// sub_service blocks with its effective AWS id already rolled up to the
// parent (see this file's package doc).
type namesDataService struct {
	// Prefix is the TF resource-type prefix this family owns, bare (no
	// "aws_", no trailing "_") - the block's own label.
	Prefix string

	// AWSID is the effective AWS SDK service identifier: the block's own
	// sdk.id when non-empty, else its own names.provider_name_upper, else
	// (sub_service only) the parent's sdk.id or provider_name_upper.
	AWSID string
}

// parseNamesData decodes names_data.hcl into its flattened family list.
func parseNamesData(raw []byte, filename string) ([]namesDataService, error) {
	parser := hclparse.NewParser()
	f, diags := parser.ParseHCL(raw, filename)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parsing %s: %s", filename, diags.Error())
	}

	var nf namesDataFile
	if diags := gohcl.DecodeBody(f.Body, nil, &nf); diags.HasErrors() {
		return nil, fmt.Errorf("decoding %s: %s", filename, diags.Error())
	}

	var out []namesDataService
	for _, svc := range nf.Services {
		id := effectiveAWSID(svc.SDK, svc.Names)
		out = append(out, namesDataService{Prefix: svc.Name, AWSID: id})

		for _, sub := range svc.SubServices {
			// Every sub_service block in the pinned file carries an empty
			// sdk.id of its own (see package doc); the parent's id is used
			// unconditionally rather than only as a fallback, so a
			// sub_service's own (never-populated-in-practice) sdk/names
			// fields can never accidentally out-vote the parent's real
			// AWS identity.
			out = append(out, namesDataService{Prefix: sub.Name, AWSID: id})
		}
	}
	return out, nil
}

// effectiveAWSID picks a block's own AWS SDK identity: sdk.id when present,
// else names.provider_name_upper.
func effectiveAWSID(sdk *nsSDKBlock, names *nsNamesBlock) string {
	if sdk != nil && sdk.ID != "" {
		return sdk.ID
	}
	if names != nil && names.ProviderNameUpper != "" {
		return names.ProviderNameUpper
	}
	return ""
}

// normalizeServiceID folds an AWS SDK identity or CFN service name down to
// a bare lowercase alphanumeric run, so "Application Auto Scaling" (sdk.id)
// and "ApplicationAutoScaling" (the CFN type segment) compare equal despite
// the spacing convention each side happens to use.
func normalizeServiceID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r - 'A' + 'a')
		}
	}
	return b.String()
}

// NamesDataMismatch is one names_data family whose AWS id does not
// normalize-match exactly one CFN service in the current registry: either
// CloudFormation has no resource family for that AWS service at all (most
// of these - low-level SDK-only clients like cognitoidp, ssooidc, s3control
// - CloudFormation folds s3control's territory into plain S3, and has never
// modeled most IDP/data-plane-only SDK surfaces as their own resource
// family), or - never observed against the pinned registry, but checked
// rather than assumed - the normalized spelling is ambiguous across more
// than one CFN service.
type NamesDataMismatch struct {
	Prefix string `json:"prefix"`
	AWSID  string `json:"aws_id"`
	Reason string `json:"reason"`
}

// deriveServiceAliases computes the generated service_aliases table: for
// every names_data family with a normalized AWS id that matches exactly one
// CFN service in cfnTypes, prefix -> that service. Families with an empty
// AWS id (never observed in the pinned file, but not assumed impossible)
// are skipped outright - not even a mismatch, since there is no id to
// report failing to match. Two families producing the same prefix would be
// a names_data.hcl surface this join cannot resolve on its own (also never
// observed against the pinned file); such a collision is reported as a
// mismatch for both rather than either one silently winning.
func deriveServiceAliases(services []namesDataService, cfnTypes []string) (map[string][]string, []NamesDataMismatch) {
	cfnByNorm := map[string][]string{}
	for _, cfn := range cfnTypes {
		svc, _, ok := splitCFNType(cfn)
		if !ok {
			continue
		}
		norm := normalizeServiceID(svc)
		found := false
		for _, existing := range cfnByNorm[norm] {
			if existing == svc {
				found = true
				break
			}
		}
		if !found {
			cfnByNorm[norm] = append(cfnByNorm[norm], svc)
		}
	}

	byPrefix := map[string][]namesDataService{}
	for _, s := range services {
		byPrefix[s.Prefix] = append(byPrefix[s.Prefix], s)
	}

	aliases := map[string][]string{}
	var mismatches []NamesDataMismatch
	for prefix, entries := range byPrefix {
		if len(entries) > 1 {
			for _, e := range entries {
				mismatches = append(mismatches, NamesDataMismatch{
					Prefix: prefix, AWSID: e.AWSID,
					Reason: fmt.Sprintf("prefix %q is claimed by more than one names_data family", prefix),
				})
			}
			continue
		}
		e := entries[0]
		if e.AWSID == "" {
			continue
		}
		norm := normalizeServiceID(e.AWSID)
		matches := cfnByNorm[norm]
		switch len(matches) {
		case 0:
			mismatches = append(mismatches, NamesDataMismatch{
				Prefix: prefix, AWSID: e.AWSID,
				Reason: "no CFN service in the current registry normalizes to the same spelling",
			})
		case 1:
			aliases[prefix] = []string{matches[0]}
		default:
			sort.Strings(matches)
			mismatches = append(mismatches, NamesDataMismatch{
				Prefix: prefix, AWSID: e.AWSID,
				Reason: fmt.Sprintf("ambiguous: normalizes to %d CFN services (%s)", len(matches), strings.Join(matches, ", ")),
			})
		}
	}
	return aliases, mismatches
}
