// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// surveyEntry is the slice of live/survey-full.json's per-type shape
// (issue #41) this tool reads: the provider's own resource identity schema,
// when v6.58.0 ships one. required_for_import is the argument-name source
// the client-named rule prefers over the carve seed and the snake-cased
// guess - it is the TF provider's own answer, not an inference this tool
// makes.
//
// Path is tools/survey-gen/classify.go's own mechanically-derived admission
// path (SURVEY.md's five-token, now seven-token, taxonomy) - issue #428's
// evidenceschema.go is the one reader in this package, added because
// Identity alone is not proof the schema names an argument this tool can
// paste: a required_for_import attribute can be server-minted (an "arn" or
// "id" the type's own block marks Computed), and Path==client-named is the
// fact that survey-gen already ran the strict, schema-safe check
// (identity.DerivableWith, over real provider schemas) that tells the two
// apart. See evidenceschema.go's own doc comment.
type surveyEntry struct {
	Type     string          `json:"type"`
	Path     string          `json:"path"`
	Identity *surveyIdentity `json:"identity"`
	Signals  surveySignals   `json:"signals"`
}

// surveySignals is the per-type signal block tools/survey-gen derives from
// the provider's own schema. Taggable is [internal/live/markers.Taggable]
// itself - survey-gen calls that function rather than reimplementing it,
// which it did until issue #285. The copy had four of the five clauses,
// missing the one #243 added: a tags map whose keys the provider documents
// as naming objects that must already exist is schema-identical to a
// free-form one and is not a marker surface. See markerless.go for the one
// rule that consults Taggable.
//
// ListResource is whether the provider ships a native list resource for
// this type (survey-gen's own signal, sourced from the provider's plugin
// framework list-resource registration). discoverablefallback.go is the one
// rule that consults it: it is one of the two independent ways a type can be
// found again without reading its identity from configuration, the other
// being live/registry.json's CloudFormation list handler joined through
// live/mapping.json.
//
// Importable is survey-gen's ImportResourceState probe (issue #331): whether
// the provider reports a classic Importer for the type at all, independently
// of whether a resource identity schema exists or the type is taggable.
// notimportable.go is the one rule that consults it - a wire identity schema
// or a taggable-based marker path can both be real and the type can still
// have no Importer behind it, which fails a live-import before either
// mechanism ever runs.
type surveySignals struct {
	Taggable     bool `json:"taggable"`
	ListResource bool `json:"list_resource"`
	Importable   bool `json:"importable"`
}

// surveyIdentity is the identity half of a survey entry: the provider's own
// resource identity schema for the type.
type surveyIdentity struct {
	RequiredForImport []string `json:"required_for_import"`
	OptionalForImport []string `json:"optional_for_import"`
}

// identityAttrs is every attribute name the provider's identity schema
// carries for this type, required and optional together, or nil when the
// provider serves no identity schema for it.
func (e surveyEntry) identityAttrs() []string {
	if e.Identity == nil {
		return nil
	}
	return append(append([]string{}, e.Identity.RequiredForImport...), e.Identity.OptionalForImport...)
}

type surveyArtifact struct {
	Types []surveyEntry `json:"types"`
}

// loadSurvey reads live/survey-full.json and indexes it by TF type name.
func loadSurvey(path string) (map[string]surveyEntry, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return nil, err
	}
	var art surveyArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	out := make(map[string]surveyEntry, len(art.Types))
	for _, e := range art.Types {
		out[e.Type] = e
	}
	return out, nil
}

// identityArg returns the single TF argument name the provider's own
// resource identity schema names for this type, and whether it applies.
// It applies only when the schema exists and requires exactly one attribute
// for import - a multi-attribute or absent schema says nothing this rule can
// use, and the caller falls through to the carve seed or the guess.
func (e surveyEntry) identityArg() (string, bool) {
	if e.Identity == nil || len(e.Identity.RequiredForImport) != 1 {
		return "", false
	}
	return e.Identity.RequiredForImport[0], true
}

// requiredForImport returns every attribute the provider's own resource
// identity schema requires for import, in the schema's own order, or nil
// when the type has no identity schema. Unlike identityArg it does not stop
// at one attribute: it is [mergeIdentityAttrs]'s source for a server-assigned
// row whose identity is composite (aws_ecs_task_definition's family +
// revision), where no single attribute alone is the row's identity but each
// is still a legitimate source [internal/live/discovery]'s importIdentity can
// read a live value from.
func (e surveyEntry) requiredForImport() []string {
	if e.Identity == nil {
		return nil
	}
	return e.Identity.RequiredForImport
}
