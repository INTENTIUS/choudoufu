// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"strings"
)

// The shape both remote stores' contracts have. GitHub issue #1442.
//
// bucketcontract.go (#1339) and kubernetescontract.go (#1393) were written
// one after the other and ended up mirroring each other by name -
// BucketFinding and ClusterFinding, SplitWaived and SplitWaivedCluster - with
// no type in common, so internal/live/projection and internal/command each
// carried two near-identical paths and each of the two silently ignored the
// other's store. A third remote store would have copied the structure again.
// What is shared is here; what is genuinely per store stays in that store's
// own file.
//
// # What is NOT shared, and why
//
// The words. Every refusal, every "what this waiver costs" clause and every
// headline is written about one kind of store and quotes what that store
// actually said. They are reached through [ContractChecker] rather than
// through parallel free functions, so a caller holding a Store it cannot
// name can still say what happened.

// Setting names one asserted property of a record store. The values are the
// names an operator writes in allow_insecure, so they are part of the
// configuration language and do not change casually. They are unique across
// stores - internal/command pins that by test - because a waiver naming a
// setting of some other backend is refused rather than quietly ignored.
type Setting string

// Outcome is what one assertion came to. Four of the five values are not a
// pass, and they are told apart because a run does something different with
// each.
type Outcome uint8

const (
	// Failed is the zero value on purpose: a finding nobody filled in has
	// not passed anything. The property was read and is wrong, which is
	// something an operator can act on, so a run refuses.
	Failed Outcome = iota

	// Passed is the store satisfying the assertion.
	Passed

	// Unreadable is a property this identity may not read but SOMEBODY can:
	// the bucket's settings behind an s3:Get* the role was not granted. A
	// run refuses, because a store nobody could check is not a store that
	// passed and the fix is a permission the role should have had.
	Unreadable

	// NotChecked is a property that cannot be answered from inside at any
	// permission level: the API server's --encryption-provider-config on a
	// managed control plane. It is never a pass, and a RUN says so and goes
	// on rather than refusing - refusing would refuse every correctly scoped
	// identity, and a gate everyone waives on their first day protects
	// nothing (#1102). A REPORT still calls the store not correct: see
	// internal/command's live-cluster.
	NotChecked

	// Warned is a finding that was read, is a concern, and is not yet a
	// breach - an identity that MAY read another estate's records on a
	// cluster where no other estate keeps any. Said out loud on every apply,
	// never a refusal.
	Warned
)

// Finding is what one setting turned out to be.
//
// One type for every store, so the callers that decide what a set of findings
// MEANS - the first-contact assertion, an apply's BeforeApply - are written
// once. Two of the fields are used by one store each, which is the price of
// that: a bucket has no verbs to review, and a cluster has no rule that
// deletes records.
type Finding struct {
	Setting Setting

	// Outcome is what the store answered. See [Outcome]: four of its five
	// values are not a pass, and which one it is decides whether a run
	// refuses or proceeds saying so.
	Outcome Outcome

	// Unwaivable is true for a failure allow_insecure does not reach. The
	// bucket has the one: an enabled lifecycle rule that expires CURRENT
	// objects under the store's keys, where the waiver's stated cost is
	// that nothing is KNOWN to expire noncurrent versions and here
	// something is known and it is destructive (GitHub issue #1377).
	Unwaivable bool

	// Found says what the store actually has, in one clause, for the
	// refusal to quote: "versioning is Suspended", "no lifecycle
	// configuration", "s3:GetBucketVersioning was denied".
	Found string

	// Verbs is the cluster contract's namespace_access review, one entry
	// per [KubernetesRecordVerbs] element, and empty for every other
	// setting and every other store.
	Verbs []VerbAccess
}

// OK reports whether the store satisfies the assertion. Everything else is
// not a pass, including a property nobody could read.
func (f Finding) OK() bool { return f.Outcome == Passed }

// ContractOptions is what a contract check needs from its caller. Each store
// reads the fields its own contract has a use for and ignores the rest, the
// way it ignores a key namespace it keeps no keys in.
type ContractOptions struct {
	// Namespaces are the store-relative key namespaces this estate writes
	// under - its records, its hint and its root outputs. The bucket's
	// lifecycle assertion needs them; see [CheckBucketContract].
	Namespaces []string

	// RequiredVerbs is what this run needs on the records themselves. Nil
	// means everything a run that WRITES records asks for, which is what
	// every caller that reached a store through an apply or a first contact
	// has. A plan-only identity names its own; see [KubernetesPlanVerbs].
	RequiredVerbs []string
}

// ContractChecker is implemented by a store that has a contract: properties
// its records depend on, which the store can read and report on, and the
// words to say when one of them does not hold. The local store does not
// implement it - a directory has no versioning, no namespace and no
// admission policy - and a store with nothing to assert is not a store that
// failed.
//
// A new remote store implements this and nothing in internal/live/projection
// or internal/command needs an edit to assert it.
type ContractChecker interface {
	// CheckContract reads every property and reports one finding per
	// setting, always all of them and always in the store's own settings
	// order: a caller that refused on the first bad one would make an
	// operator fix them one run at a time.
	//
	// The error return is for a failure that is not about the store's
	// properties at all - a cancelled context, an unreachable endpoint. A
	// denied read is NOT an error: it is a finding that did not pass.
	CheckContract(ctx context.Context, opts ContractOptions) ([]Finding, error)

	// ContractSubject is what a sentence names this store by: the noun it
	// opens with and the name it quotes, as ("Bucket", "records-prod") or
	// ("Namespace", "tofu-records-prod").
	ContractSubject() (label, value string)

	// ContractRefusal is the headline and the paragraph for one finding
	// that did not pass, in internal/command's statelessCommandRefusals
	// shape: what was refused, then what it protects against and what to do
	// instead. Empty for a finding that passed.
	ContractRefusal(f Finding) (summary, detail string)

	// ContractCheckFailed is what an apply says when the check itself could
	// not be made - the error return above, not a finding.
	ContractCheckFailed(err error) (summary, detail string)

	// ContractRefusalClosing is one line added after two or more refusals
	// in the same message, or "" for a store that needs none. The cluster
	// has one: each of its refusals carries its own allow_insecure line,
	// and a reader following two of them would write the argument twice in
	// one block, which does not parse.
	ContractRefusalClosing(refused []Setting) string
}

// AsContractChecker finds the store with a contract under s, looking through
// this package's own wrappers ([RunCache], [CountingStore]). False means
// there is nothing to assert - a local store - which is a different answer
// from a store that failed.
func AsContractChecker(s Store) (ContractChecker, bool) {
	for s != nil {
		if c, ok := s.(ContractChecker); ok {
			return c, true
		}
		u, ok := s.(interface{ Unwrap() Store })
		if !ok {
			return nil, false
		}
		s = u.Unwrap()
	}
	return nil, false
}

// SplitWaived sorts the findings that did not pass into the ones a RUN must
// refuse on, the ones it must WARN about, and the ones waived names, leaving
// passing findings out of all three. A waiver reaches exactly the settings it
// names, and silences a warning as well as a refusal: waiving one leaves a
// failure of any other in refused.
//
// A setting that could not be READ is waived by the same name as a wrong one.
// From the caller's side they are one refusal - the run cannot rely on the
// setting - and an operator whose identity cannot read what the assertion is
// about has no other way to proceed.
//
// # Why a NotChecked finding warns a run and fails a report
//
// The two callers are asking different questions and the answer differs.
//
// `choudoufu live-bucket` and `choudoufu live-cluster` ask "is this store
// correct". A property nobody could read is not a pass there: the report
// prints it, calls the store NOT correct and exits non-zero, so the operator
// who ran it on purpose goes and gets the answer from outside. Neither
// command goes through this function.
//
// A RUN asks "may I proceed". There, a refusal on [NotChecked] would refuse
// every correctly scoped identity, because scoped is exactly what makes the
// reads impossible: the Role the Kubernetes docs recommend holds Secrets in
// one namespace and cannot list kube-system's Pods. Every CI job in the
// intended arrangement would carry a waiver from its first day, which this
// repository has paid to learn protects nothing (#1102). So it warns: by
// name, with its cost, on every apply, which is #1340's whole standard for a
// thing a run proceeds past. [Unreadable] is the other side of that line and
// refuses, because somebody CAN read it and the fix is a grant.
func SplitWaived(findings []Finding, waived []string) (refused, warned, waivedFailing []Finding) {
	for _, f := range findings {
		if f.OK() {
			continue
		}
		isWaived := false
		if !f.Unwaivable {
			for _, name := range waived {
				if Setting(name) == f.Setting {
					isWaived = true
					break
				}
			}
		}
		switch {
		case isWaived:
			waivedFailing = append(waivedFailing, f)
		case f.Outcome == Warned, f.Outcome == NotChecked:
			warned = append(warned, f)
		default:
			refused = append(refused, f)
		}
	}
	return refused, warned, waivedFailing
}

// ContractRefusalText renders every failed finding as one message, each under
// its own headline, or "" when all passed. Two or more get the store's own
// closing line; see [ContractChecker.ContractRefusalClosing].
func ContractRefusalText(c ContractChecker, findings []Finding) string {
	var b strings.Builder
	var refused []Setting
	for _, f := range findings {
		summary, detail := c.ContractRefusal(f)
		if summary == "" {
			continue
		}
		refused = append(refused, f.Setting)
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(summary)
		b.WriteString(". ")
		b.WriteString(detail)
	}
	if len(refused) > 1 {
		if closing := c.ContractRefusalClosing(refused); closing != "" {
			b.WriteString("\n\n")
			b.WriteString(closing)
		}
	}
	return b.String()
}
