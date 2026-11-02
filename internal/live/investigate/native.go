// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package investigate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Native lookup ids. These are the lookups choudoufu answers itself, with no
// graph (the 2026-10-26 rulings on choudoufu#2280): the owner of a cloud ID,
// a record's history, the live resources nobody declares, and which roots a
// git range touches. "Which estates read this estate's outputs" is a graph
// question and goes to behold (the readers template).
const (
	NativeOwner    = "owner"
	NativeHistory  = "history"
	NativeForeign  = "foreign"
	NativeAffected = "affected"
)

// NativeInput is one input of a native lookup.
type NativeInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Optional    bool   `json:"optional,omitempty"`
}

// NativeFunc answers a native lookup. It returns the answer document, and
// the time the live or recorded data it read was observed when it has one.
// An error wrapping [ErrRefused] is a refusal of the question (an input
// that is not a cloud ID, an estate with no record store); any other error
// is a failure to answer.
type NativeFunc func(ctx context.Context, inputs map[string]string) (result any, observedAt string, err error)

// ErrRefused marks a refusal.
var ErrRefused = errors.New("refused")

// Refusef is a refusal with a reason.
func Refusef(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, a...))
}

// Native is one native lookup: what it answers and what that costs.
type Native struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Question  string        `json:"question"`
	Inputs    []NativeInput `json:"inputs"`
	Reads     string        `json:"reads"`
	Cost      string        `json:"cost"`
	Freshness string        `json:"freshness"`
	Via       string        `json:"via"`

	Run NativeFunc `json:"-"`
}

// Natives are the four native lookups' descriptions. The command package
// gives each its Run; a lookup with no Run is listed and refuses.
func Natives() []Native {
	return []Native{
		{
			ID:       NativeOwner,
			Title:    "Owner of a cloud ID",
			Question: "which estate and address own this cloud ID",
			Inputs: []NativeInput{
				{Name: "cloud_id", Description: "The resource's cloud ID: an AWS ARN."},
			},
			Reads:     "live markers",
			Cost:      "one live read of the resource's tags (Resource Groups Tagging API, or iam:ListRoleTags for a role), with the caller's credentials",
			Freshness: "as of this run",
			Via:       "choudoufu live marker read",
		},
		{
			ID:       NativeHistory,
			Title:    "A record's past versions",
			Question: "how has this resource's record changed over time",
			Inputs: []NativeInput{
				{Name: "address", Description: "The resource instance address, as the configuration declares it."},
				{Name: "estate", Description: "The estate whose record store is read. Defaults to the live block's.", Optional: true},
				{Name: "dir", Description: "The estate's configuration directory. Defaults to the current directory.", Optional: true},
			},
			Reads:     "record metadata",
			Cost:      "one ListObjectVersions on the record store bucket (s3:ListBucketVersions); no record payload is read",
			Freshness: "as of this run",
			Via:       "choudoufu live-history",
		},
		{
			ID:       NativeForeign,
			Title:    "Live resources nobody declares",
			Question: "which live resources does no configuration declare",
			Inputs: []NativeInput{
				{Name: "plan", Description: "A live-plan -json document (choudoufu live-plan -json > plan.json): the sweep this answer reads."},
				{Name: "type", Description: "Only resources of this type.", Optional: true},
			},
			Reads:     "a live-plan document",
			Cost:      "no cloud call: the sweep the live-plan run already made",
			Freshness: "as of the live-plan run (observed_at)",
			Via:       "choudoufu live-plan -json foreign report",
		},
		{
			ID:       NativeAffected,
			Title:    "Roots a git range touches",
			Question: "which estate roots does this git range touch",
			Inputs: []NativeInput{
				{Name: "range", Description: "The git range: A..B, A...B or A."},
				{Name: "dir", Description: "A directory inside the repository. Defaults to the current directory.", Optional: true},
			},
			Reads:     "configuration and git",
			Cost:      "configuration only: two revisions read from git, no cloud call",
			Freshness: "as of the range's revisions",
			Via:       "choudoufu live-affected",
		},
	}
}

// CheckNativeInputs checks inputs against a lookup's: every required input
// given, none extra.
func CheckNativeInputs(n Native, inputs map[string]string) error {
	known := map[string]bool{}
	var missing []string
	for _, in := range n.Inputs {
		known[in.Name] = true
		if !in.Optional && strings.TrimSpace(inputs[in.Name]) == "" {
			missing = append(missing, in.Name)
		}
	}
	var extra []string
	for k := range inputs {
		if !known[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		return fmt.Errorf("%s takes no input %s", n.ID, strings.Join(extra, ", "))
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s needs %s", n.ID, strings.Join(missing, ", "))
	}
	return nil
}

// RunNative answers one native lookup.
func RunNative(ctx context.Context, n Native, inputs map[string]string) Answer {
	if inputs == nil {
		inputs = map[string]string{}
	}
	a := Answer{
		Investigation: n.ID,
		Kind:          KindNative,
		Method:        MethodDeterministic,
		Reads:         n.Reads,
		Cost:          n.Cost,
		Freshness:     n.Freshness,
		Via:           n.Via,
		Inputs:        inputs,
	}
	if err := CheckNativeInputs(n, inputs); err != nil {
		a.Refused = err.Error()
		return a
	}
	if n.Run == nil {
		a.Unavailable = fmt.Sprintf("%s is not served by this build", n.ID)
		return a
	}
	res, observed, err := n.Run(ctx, inputs)
	a.ObservedAt = observed
	if err != nil {
		a.Refused = strings.TrimPrefix(err.Error(), ErrRefused.Error()+": ")
		return a
	}
	b, err := json.Marshal(res)
	if err != nil {
		a.Refused = "cannot render the answer: " + err.Error()
		return a
	}
	a.Result = b
	return a
}
