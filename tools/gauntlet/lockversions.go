// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
)

// LockReportEnv names the directory runOne hands each script, into which
// live/e2e/lib/gauntlet.sh's gauntlet_report_lock copies every
// .terraform.lock.hcl an init in that run wrote (issue #1739). It is how a
// row's AWSProviderVersion/KubernetesProviderVersion become a measurement
// of THIS run instead of a copy of live/oracle-versions.json's pin.
//
// The pin is what a script asks for; a lock file is what init resolved, and
// the two are not the same fact. terralith-scale's generator
// (tools/terralith-gen) writes its own hashicorp/aws constraint and never
// applies the pin, and corpus-quickpizza inits an upstream root whose
// hashicorp/kubernetes requirement is a bare lower bound. Before #1739 both
// rows recorded the pin anyway, so IsProviderStale could never fire for
// them.
const LockReportEnv = "GAUNTLET_LOCK_REPORT_DIR"

// Provider types issue #1253 records, keyed without their registry host:
// stock terraform writes registry.terraform.io/hashicorp/aws into its lock
// file and choudoufu (an OpenTofu fork) writes registry.opentofu.org/
// hashicorp/aws for the identical source, and both are the same provider
// at the same version for this purpose.
const (
	ProviderAWS        = "hashicorp/aws"
	ProviderKubernetes = "hashicorp/kubernetes"
)

// ResolvedProviders is what one run's reported lock files say it resolved:
// provider type (host stripped) -> version. A type whose reported lock
// files disagree maps to "" and is named in Conflicts - a run that
// resolved two different releases of one provider has no single version
// to record, and recording either one would be a claim about only part of
// the run.
type ResolvedProviders struct {
	Versions  map[string]string
	Conflicts []string
}

// Version returns the single version the run resolved for typ, or "" when
// no reported lock file named it or when they disagreed.
func (p ResolvedProviders) Version(typ string) string {
	return p.Versions[typ]
}

// readReportedLocks parses every file in dir as a dependency lock file and
// folds them into one ResolvedProviders. A missing or empty dir is not an
// error: it means the script reported nothing (a legacy script, one that
// died before its first init, or one whose stock init does not report),
// and the caller records no version - which reads as stale, #1253's
// "unknown reads as stale" rule - rather than falling back to the pin.
// A file that does not parse is an error, because a reported lock file the
// runner cannot read is a defect in the reporting path, not an absence.
func readReportedLocks(dir string) (ResolvedProviders, error) {
	out := ResolvedProviders{Versions: map[string]string{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	seen := map[string]map[string]bool{}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		src, err := os.ReadFile(path)
		if err != nil {
			return out, err
		}
		locked, err := parseLockVersions(src, path)
		if err != nil {
			return out, err
		}
		for typ, v := range locked {
			if seen[typ] == nil {
				seen[typ] = map[string]bool{}
			}
			seen[typ][v] = true
		}
	}
	for typ, vs := range seen {
		if len(vs) == 1 {
			for v := range vs {
				out.Versions[typ] = v
			}
			continue
		}
		var list []string
		for v := range vs {
			list = append(list, v)
		}
		sort.Strings(list)
		out.Versions[typ] = ""
		out.Conflicts = append(out.Conflicts, fmt.Sprintf("%s resolved to %s", typ, strings.Join(list, " and ")))
	}
	sort.Strings(out.Conflicts)
	return out, nil
}

// parseLockVersions reads one .terraform.lock.hcl's provider blocks:
//
//	provider "registry.terraform.io/hashicorp/aws" {
//	  version     = "6.59.0"
//	  constraints = "= 6.59.0"
//	  hashes      = [...]
//	}
//
// and returns provider type (the address with its registry host stripped)
// -> version. Only the two attributes that matter are decoded, through a
// partial schema, so a lock file carrying hashes or constraints this code
// never looks at still parses.
func parseLockVersions(src []byte, filename string) (map[string]string, error) {
	f, diags := hclparse.NewParser().ParseHCL(src, filename)
	if diags.HasErrors() {
		return nil, fmt.Errorf("%s: %s", filename, diags.Error())
	}
	content, _, diags := f.Body.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{{Type: "provider", LabelNames: []string{"source"}}},
	})
	if diags.HasErrors() {
		return nil, fmt.Errorf("%s: %s", filename, diags.Error())
	}
	out := map[string]string{}
	for _, b := range content.Blocks {
		attrs, _, diags := b.Body.PartialContent(&hcl.BodySchema{
			Attributes: []hcl.AttributeSchema{{Name: "version"}},
		})
		if diags.HasErrors() {
			return nil, fmt.Errorf("%s: %s", filename, diags.Error())
		}
		attr, ok := attrs.Attributes["version"]
		if !ok {
			continue
		}
		v, diags := attr.Expr.Value(nil)
		if diags.HasErrors() || !v.IsKnown() || v.IsNull() || !v.Type().Equals(cty.String) {
			return nil, fmt.Errorf("%s: provider %q has no readable string version", filename, b.Labels[0])
		}
		out[providerType(b.Labels[0])] = v.AsString()
	}
	return out, nil
}

// providerType strips a provider address down to namespace/type:
// "registry.opentofu.org/hashicorp/aws" and "hashicorp/aws" both give
// "hashicorp/aws".
func providerType(addr string) string {
	parts := strings.Split(addr, "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return addr
}
