// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

// Translating a Cloud Control identifier to the Terraform id a resource of the
// type is imported by (GitHub issue #2286; moved from plunge's
// pkg/backend/plunge/ccid.go, INTENTIUS/plunge#15, #19, #28 and #31, so every
// engine reads one copy). Cloud Control joins a type's primary identifier
// properties with "|", in the order the CloudFormation registry lists them
// (AWS::Cassandra::Table is "KeyspaceName|TableName"). A Terraform-schema
// engine (choudoufu, plunge's pulumi-aws) imports the Terraform id, whose
// grammar a [TypeIdentity] row spells as Components: arguments and separator
// literals ("keyspace_name" "/" "table_name").
//
// [CloudControlTranslation] picks the rule for one type:
//
//   - a single-part identifier is the id for most types, and is translated
//     for the rest (cloudcontrol_single.go);
//   - a multi-part one has a written-out translation (cloudcontrol_hand.go)
//     or follows the type's row: the translation is defined when every
//     argument component of the row is one primary identifier property,
//     matched by name (the property in snake case, "KeyspaceName" to
//     "keyspace_name", or a rename in ccRenames), and every property is used
//     exactly once. The id is then the components in the row's order with
//     each property's part put in. A row with any other component (a block, a
//     path, an optional or defaulted segment, a value of the cloud such as the
//     region) is left untranslated, with that reason, rather than guessing at
//     an id.
//
// Like the rest of this package it calls no cloud. One translation,
// aws_budgets_budget_action's, needs the account ID the engine's provider runs
// as; the engine reads it (plunge with STS on the stack's own endpoint) and
// hands it in through an [AccountSource] or [CloudControlID.WithAccount].

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

// AccountSource is how an engine supplies the AWS account ID its provider runs
// as, for a translation that needs it ([CloudControlID.NeedsAccount]). The
// engine decides where the ID comes from and which endpoint it may call;
// this package never calls one.
type AccountSource interface {
	AccountID(ctx context.Context) (string, error)
}

// AccountSourceFunc is a function as an [AccountSource].
type AccountSourceFunc func(ctx context.Context) (string, error)

// AccountID calls f.
func (f AccountSourceFunc) AccountID(ctx context.Context) (string, error) { return f(ctx) }

// ccRenames are primary identifier properties whose Terraform argument is not
// the property's snake case, by CloudFormation type.
var ccRenames = map[string]map[string]string{
	"AWS::CodePipeline::CustomActionType": {"Provider": "provider_name"},
	"AWS::Kendra::DataSource":             {"Id": "data_source_id"},
	"AWS::Kendra::Faq":                    {"Id": "faq_id"},
}

// CloudControlID turns one type's Cloud Control identifiers into its Terraform
// import ids. A nil *CloudControlID is valid and translates nothing: every
// method on it reports that no model, account or translation is needed.
type CloudControlID struct {
	tfType string
	// Each output piece is literals[i] when index[i] < 0, else the identifier
	// part at index[i].
	literals []string
	index    []int
	arity    int
	// hand is a written-out translation (cloudcontrol_hand.go), used in place
	// of literals and index.
	hand *ccHand
	// single is a single-part identifier's translation
	// (cloudcontrol_single.go), used in place of all of the above.
	single *ccSingle
	// account is the account ID the provider runs as, for a hand translation
	// that needs it.
	account string
}

// CloudControlTranslation is how tfType's Cloud Control identifiers become
// Terraform ids, for tfType listed through cfnType whose primary identifier
// properties are primary, in the registry's order. When there is none, the
// translation is nil and reason says why; the caller leaves the type
// uncovered with that reason.
func CloudControlTranslation(tfType, cfnType string, primary []string) (t *CloudControlID, reason string) {
	switch len(primary) {
	case 0:
		return nil, fmt.Sprintf("no primary identifier was given for %s", cfnType)
	case 1:
		return ccSingleTranslation(tfType, cfnType, primary[0])
	}
	if t, ok := ccHandTranslation(tfType, cfnType); ok {
		return t, ""
	}
	return ccTerraformID(tfType, cfnType, primary)
}

func (t *CloudControlID) apply(identifier string) (string, bool) {
	parts := strings.Split(identifier, "|")
	if len(parts) != t.arity {
		return "", false
	}
	var b strings.Builder
	for i, idx := range t.index {
		if idx < 0 {
			b.WriteString(t.literals[i])
			continue
		}
		if parts[idx] == "" {
			return "", false
		}
		b.WriteString(parts[idx])
	}
	return b.String(), true
}

// ccTerraformID builds the grammar translation for tfType, listed through
// cfnType whose multi-part primary identifier is primary. The reason says why
// there is none.
func ccTerraformID(tfType, cfnType string, primary []string) (*CloudControlID, string) {
	if len(primary) < 2 {
		return nil, fmt.Sprintf("%s's Cloud Control identifier has %d parts", cfnType, len(primary))
	}
	row, ok := LookupType(tfType)
	if !ok || row.ServerAssigned || len(row.Components) == 0 {
		return nil, fmt.Sprintf("choudoufu's pkg/identity has no import id grammar for %s, so %s's "+
			"multi-part Cloud Control identifier cannot be turned into its Terraform id", tfType, cfnType)
	}
	byArg := map[string]int{}
	for i, p := range primary {
		arg := snakeCase(p)
		if r, ok := ccRenames[cfnType][p]; ok {
			arg = r
		}
		byArg[arg] = i
	}
	t := &CloudControlID{tfType: tfType, arity: len(primary)}
	used := map[int]bool{}
	for _, c := range row.Components {
		if len(c.Attrs) == 0 {
			t.literals, t.index = append(t.literals, c.Literal), append(t.index, -1)
			continue
		}
		if c.Block != "" || len(c.Path) > 0 || c.OmitIfAbsent || c.Default != "" || c.Literal != "" {
			return nil, fmt.Sprintf("%s's import id has a segment that is not translated from a "+
				"Cloud Control identifier (%s)", tfType, strings.Join(c.Attrs, " or "))
		}
		idx, found := -1, false
		for _, a := range c.Attrs {
			if i, ok := byArg[a]; ok {
				idx, found = i, true
				break
			}
		}
		if !found || used[idx] {
			return nil, fmt.Sprintf("%s's import id segment %s is not one of %s's identifier "+
				"properties (%s)", tfType, strings.Join(c.Attrs, " or "), cfnType, strings.Join(primary, ", "))
		}
		used[idx] = true
		t.literals, t.index = append(t.literals, ""), append(t.index, idx)
	}
	if len(used) != len(primary) {
		return nil, fmt.Sprintf("%s's import id does not use every part of %s's identifier (%s)",
			tfType, cfnType, strings.Join(primary, ", "))
	}
	return t, ""
}

// snakeCase is a CloudFormation property name in Terraform's spelling:
// "KeyspaceName" is "keyspace_name", "VPCConnectionId" is "vpc_connection_id".
func snakeCase(s string) string {
	r := []rune(s)
	var b strings.Builder
	for i, c := range r {
		if unicode.IsUpper(c) {
			if i > 0 && (unicode.IsLower(r[i-1]) || (i+1 < len(r) && unicode.IsLower(r[i+1]) &&
				unicode.IsUpper(r[i-1]))) {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(c))
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

// ModelProperty is the model property a written-out translation reads the id
// from (an ARN the list or read handler returns), or "" when it reads none.
func (t *CloudControlID) ModelProperty() string {
	if t == nil || t.hand == nil {
		return ""
	}
	return t.hand.prop
}

// NeedsAccount reports whether the translation needs the account ID the
// provider runs as ([CloudControlID.WithAccount],
// [CloudControlID.ResolveAccount]).
func (t *CloudControlID) NeedsAccount() bool {
	return t != nil && t.hand != nil && t.hand.account
}

// WithAccount is t with the account ID its translation reads.
func (t *CloudControlID) WithAccount(account string) *CloudControlID {
	if t == nil {
		return nil
	}
	c := *t
	c.account = account
	return &c
}

// ResolveAccount is t with the account ID read from src when t needs one, and
// t itself otherwise; src is not called then. An error from src is returned as
// it is, so the engine can tell its own kinds apart (plunge's skipped
// listing).
func (t *CloudControlID) ResolveAccount(ctx context.Context, src AccountSource) (*CloudControlID, error) {
	if !t.NeedsAccount() {
		return t, nil
	}
	if src == nil {
		return nil, fmt.Errorf("%s's Terraform id needs the AWS account ID, and no account source was given",
			t.tfType)
	}
	account, err := src.AccountID(ctx)
	if err != nil {
		return nil, err
	}
	return t.WithAccount(account), nil
}

// WantsModel reports whether the caller should read the resource (Cloud
// Control GetResource) before translating identifier, because the list
// handler's model props lack what the translation reads.
func (t *CloudControlID) WantsModel(identifier string, props map[string]any) bool {
	switch {
	case t == nil:
		return false
	case t.single != nil:
		return t.single.wantsModel(identifier, props)
	case t.hand != nil && t.hand.prop != "":
		_, has := props[t.hand.prop]
		return !has
	}
	return false
}

// Describe names the id the translation produces, for an error message.
func (t *CloudControlID) Describe() string {
	if t == nil {
		return "the Terraform id"
	}
	if t.single != nil {
		return t.single.describe()
	}
	return fmt.Sprintf("%s's Terraform id (it should have %d parts)", t.tfType, t.arity)
}

// Translate is the Terraform id of one listed object: identifier is its Cloud
// Control identifier, props its model. ok is false when the identifier does not
// have the parts the translation reads or the model lacks a property it reads.
func (t *CloudControlID) Translate(identifier string, props map[string]any) (id string, ok bool) {
	if t == nil {
		return "", false
	}
	if t.single != nil {
		return t.single.translate(identifier, props)
	}
	if t.hand == nil {
		return t.apply(identifier)
	}
	parts := strings.Split(identifier, "|")
	if len(parts) != t.arity {
		return "", false
	}
	for _, p := range parts {
		if p == "" {
			return "", false
		}
	}
	prop := ""
	switch {
	case t.hand.account:
		prop = t.account
	case t.hand.prop != "":
		prop, _ = props[t.hand.prop].(string)
	}
	return t.hand.build(parts, prop)
}
