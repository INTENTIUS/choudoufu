// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// wo-sweep is issue #126's measurement probe, and nothing more: it sizes
// the attribute-level record-less-residue class across the admitted types.
//
// For every type in identity.AdmittedTypes() it reads the provider's real
// schema (via internal/live/pluginschema, no cloud calls) and reports, per
// attribute path (top-level, nested blocks, and object-typed attributes):
//
//   - hard core: WriteOnly attributes. The provider marks these precisely
//     because it never returns their values, so a stateless replan can
//     never see them echoed back. Schema-visible by construction.
//   - soft bucket: Sensitive AND settable (Optional or Required) and not
//     WriteOnly. These may or may not read back; the schema alone cannot
//     say, so they are reported separately, not merged into the core.
//   - computed-sensitive exports: Sensitive AND Computed-only. Not config
//     arguments at all, but the aws_iam_access_key.secret shape (#125)
//     lives here: created-once values no read recovers.
//
// It also checks the two proven instances (#105's aws_s3_object.content,
// #125's aws_iam_access_key.secret) against the sweep's own buckets, so
// the output states what a schema-driven lint rule could and could not
// catch.
//
// Throwaway measurement tooling: this feeds one issue comment and is not
// part of any generation pipeline.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/pluginschema"
)

type attrFinding struct {
	Path      string `json:"path"`
	Required  bool   `json:"required"`
	Optional  bool   `json:"optional"`
	Computed  bool   `json:"computed"`
	Sensitive bool   `json:"sensitive"`
	WriteOnly bool   `json:"write_only"`
}

type typeFinding struct {
	Type              string        `json:"type"`
	Hard              []attrFinding `json:"hard,omitempty"`
	SensitiveSettable []attrFinding `json:"sensitive_settable,omitempty"`
	ComputedSensitive []attrFinding `json:"computed_sensitive,omitempty"`
}

type output struct {
	Provider        string        `json:"provider"`
	ProviderVersion string        `json:"provider_version"`
	AdmittedTypes   int           `json:"admitted_types"`
	MissingSchema   []string      `json:"missing_schema,omitempty"`
	TypesWithHard   int           `json:"types_with_hard"`
	HardAttrs       int           `json:"hard_attrs"`
	HardRequired    int           `json:"hard_required"`
	TypesWithSoft   int           `json:"types_with_sensitive_settable"`
	SoftAttrs       int           `json:"sensitive_settable_attrs"`
	TypesWithCompSn int           `json:"types_with_computed_sensitive"`
	CompSnAttrs     int           `json:"computed_sensitive_attrs"`
	ProvenInstances []string      `json:"proven_instances"`
	Findings        []typeFinding `json:"findings"`
}

func walkBlock(b *configschema.Block, prefix string, tf *typeFinding) {
	names := make([]string, 0, len(b.Attributes))
	for n := range b.Attributes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		a := b.Attributes[n]
		f := attrFinding{
			Path:      prefix + n,
			Required:  a.Required,
			Optional:  a.Optional,
			Computed:  a.Computed,
			Sensitive: a.Sensitive,
			WriteOnly: a.WriteOnly,
		}
		settable := a.Required || a.Optional
		switch {
		case a.WriteOnly:
			tf.Hard = append(tf.Hard, f)
		case a.Sensitive && settable:
			tf.SensitiveSettable = append(tf.SensitiveSettable, f)
		case a.Sensitive && a.Computed && !settable:
			tf.ComputedSensitive = append(tf.ComputedSensitive, f)
		}
		if a.NestedType != nil {
			walkObject(a.NestedType, prefix+n+".", tf)
		}
	}
	blockNames := make([]string, 0, len(b.BlockTypes))
	for n := range b.BlockTypes {
		blockNames = append(blockNames, n)
	}
	sort.Strings(blockNames)
	for _, n := range blockNames {
		walkBlock(&b.BlockTypes[n].Block, prefix+n+".", tf)
	}
}

func walkObject(o *configschema.Object, prefix string, tf *typeFinding) {
	names := make([]string, 0, len(o.Attributes))
	for n := range o.Attributes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		a := o.Attributes[n]
		f := attrFinding{
			Path:      prefix + n,
			Required:  a.Required,
			Optional:  a.Optional,
			Computed:  a.Computed,
			Sensitive: a.Sensitive,
			WriteOnly: a.WriteOnly,
		}
		settable := a.Required || a.Optional
		switch {
		case a.WriteOnly:
			tf.Hard = append(tf.Hard, f)
		case a.Sensitive && settable:
			tf.SensitiveSettable = append(tf.SensitiveSettable, f)
		case a.Sensitive && a.Computed && !settable:
			tf.ComputedSensitive = append(tf.ComputedSensitive, f)
		}
		if a.NestedType != nil {
			walkObject(a.NestedType, prefix+n+".", tf)
		}
	}
}

func main() {
	initBin := flag.String("init-bin", "terraform", "binary used to install the provider")
	version := flag.String("version", "6.59.0", "provider version to sweep")
	flag.Parse()

	workdir, err := os.MkdirTemp("", "wo-sweep")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(workdir)

	schemas, err := pluginschema.ResourceTypes(context.Background(), pluginschema.Request{
		InitBin:  *initBin,
		WorkDir:  workdir,
		Source:   "hashicorp/aws",
		Version:  *version,
		Provider: addrs.NewDefaultProvider("aws"),
		Log:      os.Stderr,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	out := output{
		Provider:        "hashicorp/aws",
		ProviderVersion: *version,
	}
	admitted := identity.AdmittedTypes()
	out.AdmittedTypes = len(admitted)

	for _, t := range admitted {
		s, ok := schemas[t]
		if !ok || s.Block == nil {
			out.MissingSchema = append(out.MissingSchema, t)
			continue
		}
		tf := typeFinding{Type: t}
		walkBlock(s.Block, "", &tf)
		if len(tf.Hard) > 0 {
			out.TypesWithHard++
			out.HardAttrs += len(tf.Hard)
			for _, a := range tf.Hard {
				if a.Required {
					out.HardRequired++
				}
			}
		}
		if len(tf.SensitiveSettable) > 0 {
			out.TypesWithSoft++
			out.SoftAttrs += len(tf.SensitiveSettable)
		}
		if len(tf.ComputedSensitive) > 0 {
			out.TypesWithCompSn++
			out.CompSnAttrs += len(tf.ComputedSensitive)
		}
		if len(tf.Hard)+len(tf.SensitiveSettable)+len(tf.ComputedSensitive) > 0 {
			out.Findings = append(out.Findings, tf)
		}
	}

	// The two proven instances, reported raw so the comment can say
	// whether the sweep's buckets catch them.
	for _, probe := range []struct{ typ, attr string }{
		{"aws_s3_object", "content"},
		{"aws_iam_access_key", "secret"},
	} {
		s, ok := schemas[probe.typ]
		if !ok || s.Block == nil {
			out.ProvenInstances = append(out.ProvenInstances,
				fmt.Sprintf("%s.%s: type not in provider schema", probe.typ, probe.attr))
			continue
		}
		a, ok := s.Block.Attributes[probe.attr]
		if !ok {
			out.ProvenInstances = append(out.ProvenInstances,
				fmt.Sprintf("%s.%s: attribute not in schema", probe.typ, probe.attr))
			continue
		}
		out.ProvenInstances = append(out.ProvenInstances,
			fmt.Sprintf("%s.%s: required=%v optional=%v computed=%v sensitive=%v write_only=%v",
				probe.typ, probe.attr, a.Required, a.Optional, a.Computed, a.Sensitive, a.WriteOnly))
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
