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

	"github.com/intentius/choudoufu/internal/live/mdspan"
)

var markers = mdspan.For("iamref-gen")

const (
	markersMDRel    = "live/MARKERS.md"
	spanResourceTag = "resource-tag-services"
)

// renderResourceTagSpan writes the roster of services AWS's Service
// Authorization Reference names aws:ResourceTag on into live/MARKERS.md's
// estate-grant section (issue #142).
func renderResourceTagSpan(root string, rows []Row) error {
	path := filepath.Join(root, markersMDRel)
	doc, err := os.ReadFile(path) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return fmt.Errorf("reading %s: %w", markersMDRel, err)
	}

	out, err := markers.Replace(markersMDRel, string(doc), spanResourceTag, resourceTagSpanBody(rows))
	if err != nil {
		return err
	}
	if out == string(doc) {
		return nil
	}
	return os.WriteFile(path, []byte(out), 0o644) //nolint:gosec // a committed doc
}

// resourceTagSpanBody renders the roster. It is pure so the drift test can
// re-derive it from the shipped artifact and compare against the shipped
// document; nothing else keeps those two in step, since the span only moves
// when someone runs the generator, and a stale roster in a section about what
// IAM will actually evaluate is the exact failure the section warns about.
//
// One direction only, and the closing sentence says so in the document. The
// reference is authoritative about the condition keys it names and not about
// the ones it omits - lambda:GetFunction lists none at all while Lambda does
// support tag-based authorization - so this is the set where a marker-scoped
// grant is KNOWN to bite. The services absent from it are unmeasured, not
// disproven, and rendering them as a second column would be inventing a fact.
func resourceTagSpanBody(rows []Row) string {
	// Grouped by IAM prefix, not by CloudFormation service segment. The
	// action counts are the reference's, and the reference is indexed by IAM
	// prefix - so ResilienceHub and ResilienceHubV2, which both resolve to
	// "resiliencehub", are one row of 57 actions rather than two. Rendering
	// them separately shows the same 57 twice and inflates the denominator.
	type group struct {
		prefix   string
		services []string
		listing  int
		total    int
	}
	byPrefix := map[string]*group{}
	for _, r := range rows {
		if r.IAMPrefix == "" {
			continue
		}
		g := byPrefix[r.IAMPrefix]
		if g == nil {
			g = &group{prefix: r.IAMPrefix, listing: r.ActionsListingResourceTag, total: r.ActionsTotal}
			byPrefix[r.IAMPrefix] = g
		}
		g.services = append(g.services, r.Service)
	}

	var listed []*group
	for _, g := range byPrefix {
		sort.Strings(g.services)
		if g.listing > 0 {
			listed = append(listed, g)
		}
	}
	sort.Slice(listed, func(i, j int) bool {
		if listed[i].listing != listed[j].listing {
			return listed[i].listing > listed[j].listing
		}
		return listed[i].prefix < listed[j].prefix
	})

	var b strings.Builder
	b.WriteString("| Service | IAM prefix | Actions naming `aws:ResourceTag` |\n|---|---|---|\n")
	for _, g := range listed {
		fmt.Fprintf(&b, "| %s | `%s` | %d of %d |\n",
			strings.Join(g.services, ", "), g.prefix, g.listing, g.total)
	}
	fmt.Fprintf(&b, "\n%d of the %d IAM prefixes this estate's admitted types reach name the key on at "+
		"least one action. The remaining %d are **unmeasured, not disproven**: the reference does not "+
		"set out to enumerate every global condition key per action, and `lambda:GetFunction` lists none "+
		"at all while Lambda does support tag-based authorization. Read this as the set a marker-scoped "+
		"grant is known to bite on, never as its complement.\n",
		len(listed), len(byPrefix), len(byPrefix)-len(listed))
	return b.String()
}

// spanSCPActions is the SCP's action list in live/MARKERS.md.
const spanSCPActions = "scp-untag-actions"

// renderSCPActionsSpan writes the tag-removal actions an SCP can deny for
// the marker keys, replacing the hand-written eight-action list that section
// carried with a caveat saying nothing could check it (issue #152).
//
// There is something to check it against now. live/tag-verbs.json resolves
// each service's removal verb from botocore's own service models, and this
// artifact says whether AWS's Service Authorization Reference names
// aws:TagKeys on it - which is the condition the Deny is written against.
func renderSCPActionsSpan(root string, rows []Row) error {
	path := filepath.Join(root, markersMDRel)
	doc, err := os.ReadFile(path) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return fmt.Errorf("reading %s: %w", markersMDRel, err)
	}
	out, err := markers.Replace(markersMDRel, string(doc), spanSCPActions, scpActionsSpanBody(rows))
	if err != nil {
		return err
	}
	if out == string(doc) {
		return nil
	}
	return os.WriteFile(path, []byte(out), 0o644) //nolint:gosec // a committed doc
}

// scpActionsSpanBody renders the action list, pure for the drift test.
//
// Split in two, and the split is the point. Actions the reference names
// aws:TagKeys on are ones where the Deny is known to be evaluated. Actions
// it does not name it on go in their own list rather than being dropped:
// dropping them would silently narrow the policy, and listing them beside
// the others would be the exact failure this section warns about - a
// statement that looks correct and does nothing.
func scpActionsSpanBody(rows []Row) string {
	var honoured, unlisted, noVerb []string
	for _, r := range rows {
		if r.IAMPrefix == "" {
			continue
		}
		if r.UntagAction == "" {
			noVerb = append(noVerb, r.Service)
			continue
		}
		if !r.UntagActionFound {
			continue
		}
		qualified := r.IAMPrefix + ":" + r.UntagAction
		if r.UntagListsTagKeys {
			honoured = append(honoured, qualified)
		} else {
			unlisted = append(unlisted, qualified)
		}
	}
	honoured = dedupeSorted(honoured)
	unlisted = dedupeSorted(unlisted)

	var b strings.Builder
	fmt.Fprintf(&b, "%d tag-removal actions across this estate's services name `aws:TagKeys` in the "+
		"Service Authorization Reference, so a `Deny` conditioned on it is evaluated for them. "+
		"Each service's removal verb is resolved from botocore's own service models "+
		"(`live/tag-verbs.json`), not written by hand.\n\n", len(honoured))

	b.WriteString("<details>\n<summary>The full action list, for pasting into the policy above</summary>\n\n```json\n")
	b.WriteString("\"Action\": [\n")
	for i, a := range honoured {
		comma := ","
		if i == len(honoured)-1 {
			comma = ""
		}
		fmt.Fprintf(&b, "  %q%s\n", a, comma)
	}
	b.WriteString("]\n```\n\n</details>\n")

	if len(unlisted) > 0 {
		fmt.Fprintf(&b, "\n**%d do not name it, and these are where the warning above actually bites:** %s. "+
			"The reference is silent rather than negative here, so this is not proof the `Deny` fails - "+
			"but it is the difference between a statement checked and a statement assumed, and these are "+
			"the ones to verify against the service's own reference page before relying on them. "+
			"`route53:ChangeTagsForResource` is the combined add-and-remove call this section already "+
			"singled out as worth checking; the measurement agrees.\n", len(unlisted), backticked(unlisted))
	}
	if len(noVerb) > 0 {
		fmt.Fprintf(&b, "\n%d further services have no removal verb resolved in `live/tag-verbs.json` at all, "+
			"either because the service's model offers more than one candidate or none. They are absent from "+
			"the list above rather than silently covered by it.\n", len(noVerb))
	}
	return b.String()
}

// dedupeSorted sorts and removes duplicates: several CloudFormation service
// segments can share one IAM prefix, and the policy wants the action once.
func dedupeSorted(in []string) []string {
	sort.Strings(in)
	out := in[:0]
	for i, v := range in {
		if i == 0 || v != in[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// backticked renders a list for prose.
func backticked(in []string) string {
	q := make([]string, len(in))
	for i, v := range in {
		q[i] = "`" + v + "`"
	}
	return strings.Join(q, " and ")
}
