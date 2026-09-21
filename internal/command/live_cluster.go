// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveClusterCommand reports whether the cluster a record_store "kubernetes"
// keeps its records in satisfies its contract (GitHub issue #1393): the
// records namespace and this identity's access to it, read isolation,
// encryption at rest, and the estate boundary policy.
//
// It is `live-bucket` (#1341) for the other remote store, and it answers the
// same question in the same way and for the same reason.
//
// # It reports the cluster, not the configuration
//
// A plan or an apply honours allow_insecure (#1340), because whether a RUN
// may proceed is the operator's call. This command answers a different
// question - is this cluster correct - and answering it through the run's
// path would print green for a cluster whose Secrets are not encrypted,
// which is the opposite of what someone runs it for. So a waiver never
// changes a verdict or the exit status here. It is named, separately,
// together with whether it is hiding anything.
//
// # Not checked is not correct
//
// Two of the four properties cannot be read on every distribution. A finding
// the cluster could not answer is NOT CHECKED, it is not a pass, and it
// makes the verdict "NOT correct" the same way a failure does. That is the
// whole reason for a report a person runs on purpose: an operator who wants
// the answer can go and get it from outside the cluster.
//
// It writes nothing: no sentinel, no record, no Secret. The only object it
// creates is a SelfSubjectAccessReview, which is a question, not a change -
// the API server evaluates it and stores nothing.
type LiveClusterCommand struct {
	Meta
}

// liveClusterReport is the -json document, and the source of the table.
type liveClusterReport struct {
	Namespace string `json:"namespace"`
	Estate    string `json:"estate"`
	Correct   bool   `json:"correct"`
	// Warnings counts the findings that are a concern and not a refusal. A
	// run proceeds past every one of them, so they do not change Correct,
	// and the verdict line names the count rather than letting a green
	// swallow them.
	Warnings int `json:"warnings"`
	// CheckedAs is "apply" or "plan": which run's verbs the namespace_access
	// assertion required. The two can disagree, so the report says which
	// question it answered.
	CheckedAs string                   `json:"checked_as"`
	Settings  []liveClusterSettingLine `json:"settings"`
	// Waived is what the configuration's allow_insecure names, empty with
	// -namespace or with no waiver. It never affects Correct.
	Waived []liveWaiverLine `json:"waived"`
}

type liveClusterSettingLine struct {
	Setting string `json:"setting"`
	// Verdict is "ok", "fail" or "not_checked".
	Verdict string `json:"verdict"`
	Found   string `json:"found"`
	// Verbs is the namespace_access review, one entry per verb, and empty
	// for every other setting.
	Verbs []staterecord.VerbAccess `json:"verbs,omitempty"`
}

func (c *LiveClusterCommand) Run(rawArgs []string) int {
	ctx := c.CommandContext()
	common, rawArgs := arguments.ParseView(rawArgs)
	c.View.Configure(common)

	args, diags := arguments.ParseLiveCluster(rawArgs)
	if diags.HasErrors() {
		c.View.Diagnostics(diags)
		return 1
	}
	c.Meta.input = false

	namespace, estate := args.Namespace, args.Estate
	var rs *configs.LiveRecordStore
	if namespace == "" {
		live, liveDiags := c.statelessSettings(ctx, false)
		diags = diags.Append(liveDiags)
		switch {
		case liveDiags.HasErrors():
		case live == nil || live.RecordStore == nil || live.RecordStore.Type != "kubernetes":
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "No record store cluster here",
				"This directory's configuration declares no record_store \"kubernetes\" block, so there is no records namespace to report on. Name one with -namespace=<name>."))
		default:
			rs = live.RecordStore
			if estate == "" {
				estate = live.Estate
			}
		}
		if diags.HasErrors() {
			c.View.Diagnostics(diags)
			return 1
		}
	}

	requiredVerbs := staterecord.KubernetesRecordVerbs
	checkedAs := "apply"
	if args.PlanIdentity {
		requiredVerbs = staterecord.KubernetesPlanVerbs
		checkedAs = "plan"
	}

	findings, ns, err := projection.VerifyCluster(ctx, rs, estate, namespace, requiredVerbs)
	if err != nil {
		c.View.Diagnostics(diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot read the cluster",
			fmt.Sprintf("The records namespace %q could not be checked: %s. This says nothing about whether the cluster is correct.", ns, err))))
		return 1
	}

	report := buildLiveClusterReport(ns, estate, checkedAs, findings, rs)

	if args.JSON {
		out, jsonErr := json.MarshalIndent(report, "", "  ")
		if jsonErr != nil {
			c.View.Diagnostics(diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot render the report", jsonErr.Error())))
			return 1
		}
		views.NewLiveCluster(c.View).Output(string(out))
	} else {
		views.NewLiveCluster(c.View).Output(renderLiveClusterReport(report))
	}
	if !report.Correct {
		return 1
	}
	return 0
}

// buildLiveClusterReport is the whole of this command's judgement, kept apart
// from the cluster call so it can be held to the one rule that matters:
// Correct comes from the findings alone, and a finding that could not be
// answered is not a pass. rs is nil with -namespace.
func buildLiveClusterReport(namespace, estate, checkedAs string, findings []staterecord.Finding, rs *configs.LiveRecordStore) liveClusterReport {
	report := liveClusterReport{
		Namespace: namespace, Estate: estate, Correct: true, CheckedAs: checkedAs,
		Settings: []liveClusterSettingLine{},
	}
	failing := map[staterecord.Setting]bool{}
	for _, f := range findings {
		verdict := "ok"
		switch f.Outcome {
		case staterecord.Warned:
			verdict = "warn"
		case staterecord.NotChecked:
			verdict = "not_checked"
		case staterecord.Passed:
		default:
			verdict = "fail"
		}
		if !f.OK() {
			failing[f.Setting] = true
			// A warning is a concern and not a failure: a run proceeds past
			// it, so a report that called the cluster NOT correct for one
			// would disagree with every apply. It is printed, counted, and
			// named in the verdict line.
			if f.Outcome == staterecord.Warned {
				report.Warnings++
			} else {
				report.Correct = false
			}
		}
		report.Settings = append(report.Settings, liveClusterSettingLine{
			Setting: string(f.Setting), Verdict: verdict, Found: f.Found, Verbs: f.Verbs,
		})
	}
	report.Waived = liveWaiverLines(rs, failing)
	return report
}

func renderLiveClusterReport(r liveClusterReport) string {
	var b strings.Builder
	for _, s := range r.Settings {
		fmt.Fprintf(&b, "  %-19s %-12s %s\n", s.Setting, strings.ToUpper(s.Verdict), s.Found)
		for _, v := range s.Verbs {
			answer := "denied"
			if v.Allowed {
				answer = "allowed"
			}
			needed := "not needed by this run"
			if v.Required {
				needed = "needed by this run"
			}
			fmt.Fprintf(&b, "      secrets %-7s %-8s (%s)\n", v.Verb, answer, needed)
		}
	}
	renderLiveWaiverLines(&b, r.Waived, "cluster")
	if r.CheckedAs == "plan" {
		b.WriteString("  checked as a plan identity: secrets get and list are required, create, update and delete are reported and not required. An apply needs all five.\n")
	} else {
		b.WriteString("  checked as an apply identity: all five of the store's verbs on secrets are required. -plan-identity asks what a plan job needs instead.\n")
	}
	b.WriteString("\n")
	if r.Correct {
		fmt.Fprintf(&b, "records namespace %s: correct", r.Namespace)
	} else {
		fmt.Fprintf(&b, "records namespace %s: NOT correct", r.Namespace)
		for _, s := range r.Settings {
			if s.Verdict == "not_checked" {
				// Said only when one of them IS not checked, so the sentence
				// is never explaining a verdict that came from somewhere
				// else.
				b.WriteString(" (a NOT_CHECKED property is not a pass)")
				break
			}
		}
	}
	if r.Warnings > 0 {
		fmt.Fprintf(&b, ", with %d warning(s) a run proceeds past", r.Warnings)
	}
	return b.String()
}

func (c *LiveClusterCommand) Help() string {
	return strings.TrimSpace(`
Usage: choudoufu [global options] live-cluster [options]

  Reports whether the cluster a record_store "kubernetes" keeps its records
  in satisfies the four things those records depend on: the records
  namespace and this identity's access to Secrets in it, read isolation
  from other estates, encryption at rest, and the estate boundary policy.
  Exits non-zero unless all four hold.

  Run with no options in a configuration directory to check the namespace
  its live block resolves to, or name any namespace with -namespace.

  This reports the CLUSTER, not the configuration. An allow_insecure waiver
  lets a plan or an apply proceed; it never changes a verdict here. A
  configured waiver is named separately, with whether it is hiding a
  failure.

  Two of the four cannot be read on every distribution. Encryption at rest
  is an API server flag, readable only where the API server's own Pod is
  (kind, kubeadm) and never on a managed control plane; the estate boundary
  policy needs cluster-scoped get. Either one unanswered is reported
  NOT_CHECKED, which is not a pass and makes the verdict NOT correct.

  Nothing is written. The access questions go to the API server as
  SelfSubjectAccessReviews, which change nothing, never as an attempted
  write.

Options:

  -namespace=name
                 The records namespace to check, instead of the one the
                 configuration resolves to.
  -estate=name   The estate whose records live there. Without -namespace it
                 is only reported; with one, and with no configuration, it
                 is what the default namespace name is derived from.
  -plan-identity Ask what a PLAN job's identity needs - get and list on
                 Secrets - instead of what an apply needs. The other three
                 verbs are still reported. The report says which of the two
                 it did.
  -json          One JSON document on stdout.
`)
}

func (c *LiveClusterCommand) Synopsis() string {
	return "Report whether a record store cluster keeps its contract"
}
