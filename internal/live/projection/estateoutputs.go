// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is GitHub issue #1371's reader: one estate reading the root
// output values another estate recorded under tofu-outputs/<other>/, through
// a `data "terraform_estate_outputs"` block that names the other estate and
// the outputs it reads. live/OUTPUTS.md, "What an output read is for", is the
// decision this implements and bounds: data sources on the producer's live
// resource stay the rule, and this read is for a value with no live resource
// behind it.
//
// # Why this is not ReadRootOutputValues
//
// [ReadRootOutputValues] logs and skips every error, and it is right to: it
// reads this estate's OWN previous values, and the cost of a skipped one is a
// "+" line on a plan. A value read from another estate is used as input, so
// a skipped read would be a configuration evaluating against nothing, and a
// denied read would surface as a bare "unsupported attribute" several lines
// away from its cause. Every failure here is a diagnostic, and a denial names
// the other estate and the grant that is missing.
//
// # What crosses
//
// Exactly what the producer wrote: [WriteRootOutputValues] never writes a
// sensitive output or one whose value is not wholly known, so neither can be
// read here. Nothing is listed. The consumer names each output it reads, so
// the read is bounded by the consumer's configuration the way the producer's
// own read is bounded by its own.

const (
	// SummaryEstateOutputsDenied is the refusal for a read the record store
	// refused by policy: the consumer's identity has no grant on the other
	// estate's tofu-outputs/ prefix.
	SummaryEstateOutputsDenied = "This estate may not read another estate's outputs"

	// SummaryEstateOutputsUnreadable covers every other failure to read: the
	// store unreachable or not opened, a KMS key that refused or cannot be
	// used, a record that will not decode, an estate name outside the marker
	// grammar.
	SummaryEstateOutputsUnreadable = "Cannot read another estate's outputs"

	// SummaryEstateOutputNotRecorded is a named output with no record.
	SummaryEstateOutputNotRecorded = "Another estate has not recorded this output"

	// SummaryEstateOutputsSelf is a read that names the reading estate.
	SummaryEstateOutputsSelf = "An estate cannot read its own outputs this way"

	// SummaryEstateOutputsUndeclared is a read, through a "kubernetes"
	// record store, of an estate its record_store block does not declare
	// with a reads_outputs_of block. Each estate's records are in a
	// namespace of their own, and this run opens another estate's only when
	// the configuration names it.
	SummaryEstateOutputsUndeclared = "This estate does not declare that it reads another estate's outputs"

	// SummaryEstateOutputsAsOf is the warning every successful read raises:
	// the values are a copy as of the producer's last apply.
	SummaryEstateOutputsAsOf = "Values from another estate are as of its last apply"
)

// EstateOutputsSource is the consumer's side of a cross-estate output read:
// the record store this run opened, and enough about it to say what is
// missing when the read is refused.
type EstateOutputsSource struct {
	// Store is this run's record store. Nil when the run has none open,
	// which refuses every read rather than answering "not recorded".
	Store staterecord.Store

	// Estate is the reading estate's name.
	Estate string

	// StoreType is the live block's record_store label ("s3", "local",
	// "kubernetes"), and Bucket the s3 bucket when there is one. Both are
	// only used to word the remedy.
	StoreType string
	Bucket    string

	// Unavailable, when Store is nil, says why, for the refusal's detail.
	Unavailable string

	// Declared is, for a "kubernetes" store, the reads its record_store
	// block declares with reads_outputs_of blocks: each other estate's name
	// to the namespace its records are read from. A read of an estate not
	// in it is refused ([SummaryEstateOutputsUndeclared]) rather than sent
	// to this estate's own namespace, where it could only answer "not
	// recorded", or to a namespace this configuration never named.
	//
	// Unused by the other backends: on "s3" every estate's outputs are in
	// the one bucket and the declaration is the bucket policy's.
	Declared map[string]string

	// OpenDeclared opens a read-only store on estate other's records in
	// namespace, for a declared read. Non-nil is also what says this store
	// keeps each estate's records apart, so a read goes only to an estate in
	// Declared: [NewEstateOutputsSource] sets it from the record_store
	// block's own decoded property (configs.LiveRecordStore.OutputReadsDeclared)
	// rather than from a backend name, and a test sets it to a fake cluster.
	OpenDeclared func(ctx context.Context, other, namespace string) (staterecord.Store, error)
}

// ReadEstateOutputs reads each of names from estate other's recorded root
// outputs. It returns the values keyed by name, or error diagnostics and no
// values. A successful read also returns one warning,
// [SummaryEstateOutputsAsOf], saying when the producer recorded them.
func ReadEstateOutputs(ctx context.Context, src EstateOutputsSource, other string, names []string) (map[string]cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	if !markers.ValidEstateName(other) {
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryEstateOutputsUnreadable, fmt.Sprintf(
			"The estate name %q does not match the tofu-estate marker grammar in live/MARKERS.md (a lowercase letter followed by lowercase letters, digits or hyphens), so it cannot name an estate whose outputs could have been recorded.",
			other)))
	}
	if other == src.Estate {
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryEstateOutputsSelf, fmt.Sprintf(
			"This configuration is estate %q, and the block reads estate %q's outputs. An estate's own root outputs are values it computes; read the expression the output is built from instead.",
			src.Estate, other)))
	}
	// On Kubernetes the declaration is checked before anything else about
	// the store: it is a fact about the configuration, true or false
	// whether the cluster is reachable, and an undeclared read is the one
	// that must never fall through to this estate's own namespace.
	var namespace string
	if src.OpenDeclared != nil {
		ns, declared := src.Declared[other]
		if !declared {
			return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryEstateOutputsUndeclared, fmt.Sprintf(
				"A data \"terraform_estate_outputs\" block reads estate %q's outputs, and this estate's record_store \"kubernetes\" block declares no read of %q. Each estate keeps its records in a namespace of its own, and this run opens another estate's namespace only when the configuration names it, so the read was not attempted. Declare it in the record_store block:\n\n  reads_outputs_of %q {}\n\nwhich reads from namespace %q, the default for that estate; add namespace = \"<name>\" inside the block when %q keeps its records elsewhere. The read needs get on %q's output Secrets in that namespace, and a refusal names the grant.",
				other, other, other, KubernetesRecordNamespace(other), other, other)))
		}
		namespace = ns
	}

	if src.Store == nil {
		why := src.Unavailable
		if why == "" {
			why = "this run has no record store open"
		}
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryEstateOutputsUnreadable, fmt.Sprintf(
			"Estate %q's outputs are read from the record store, under %s, and %s.",
			other, RootOutputKeyPrefix(other), why)))
	}

	sorted := append([]string(nil), names...)
	sort.Strings(sorted)

	readFrom := src.Store
	if src.OpenDeclared != nil {
		opened, err := src.OpenDeclared(ctx, other, namespace)
		if err != nil {
			return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryEstateOutputsUnreadable, fmt.Sprintf(
				"Estate %q's outputs are declared as read from namespace %q, and a store there could not be opened: %s.",
				other, namespace, err)))
		}
		readFrom = opened
	}

	store := &RootOutputStore{store: readFrom, estate: other}
	values := make(map[string]cty.Value, len(sorted))
	var oldest time.Time
	var undated []string
	for _, name := range sorted {
		val, recordedAt, _, exists, err := store.get(ctx, name)
		if err != nil {
			return nil, diags.Append(estateOutputsReadDiag(src, namespace, other, name, err))
		}
		if !exists {
			where := RootOutputKey(other, name)
			if namespace != "" {
				where = fmt.Sprintf("%s, in namespace %q", where, namespace)
			}
			return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryEstateOutputNotRecorded, fmt.Sprintf(
				"Estate %q has no recorded value for its output %q (%s). An estate records a root output's value at the end of each apply, so this means one of: %q has not applied since it declared the output; it declares no output by that name; or the output is marked sensitive or its value was not wholly known, and neither is ever recorded, so neither can cross to another estate.",
				other, name, where, other)))
		}
		values[name] = val
		if recordedAt.IsZero() {
			undated = append(undated, name)
		} else if oldest.IsZero() || recordedAt.Before(oldest) {
			oldest = recordedAt
		}
	}

	var when string
	switch {
	case len(undated) == len(sorted):
		when = "at a time the record does not carry (it was written before this build recorded one)"
	case len(undated) > 0:
		when = fmt.Sprintf("at %s at the earliest (%s carry no time: written before this build recorded one)", oldest.Format(time.RFC3339), joinQuoted(undated))
	default:
		when = "at " + oldest.Format(time.RFC3339)
		if len(sorted) > 1 {
			when += " at the earliest"
		}
	}
	subject := "The value of " + joinQuoted(sorted) + " was"
	if len(sorted) > 1 {
		subject = "The values of " + joinQuoted(sorted) + " were"
	}
	diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryEstateOutputsAsOf, fmt.Sprintf(
		"%s read from estate %q as recorded by its last apply, %s. A recorded value is a copy and changes only when %q applies again, so a value that mirrors a live attribute, changed since without an apply, is not reflected here: read that kind of value with a data source on the live resource (live/OUTPUTS.md).",
		subject, other, when, other)))
	return values, diags
}

// estateOutputsReadDiag is the error for a record that could not be read.
// It follows internal/command's recordStoreOpenDiag: a denial and a KMS
// refusal each get wording of their own, because each sends the reader to a
// different place to fix it.
//
// namespace is the Kubernetes namespace a declared read went to, and "" for
// every other backend.
func estateOutputsReadDiag(src EstateOutputsSource, namespace, other, name string, err error) tfdiags.Diagnostic {
	key := RootOutputKey(other, name)
	if namespace != "" {
		// A declared read's namespace is the other estate's, which this
		// estate neither creates nor writes to, so the store's own remedy
		// for a missing namespace (create it, grant every verb) is the
		// wrong one to hand the reader.
		var missing *staterecord.NamespaceMissingError
		var terminating *staterecord.NamespaceTerminatingError
		if errors.As(err, &missing) || errors.As(err, &terminating) {
			return tfdiags.Sourceless(tfdiags.Error, SummaryEstateOutputsUnreadable, fmt.Sprintf(
				"Reading estate %q's output %q: the namespace this estate's reads_outputs_of %q block reads from, %q, does not exist or is being deleted. Either %q keeps its records in another namespace, and the block's namespace argument has to name it, or %q has never applied with record_store \"kubernetes\" in this cluster.\n\nWhat the store said: %s.",
				other, name, other, namespace, other, other, err))
		}
	}
	var kmsDenied *staterecord.KMSDeniedError
	if errors.As(err, &kmsDenied) {
		return tfdiags.Sourceless(tfdiags.Error, SummaryEstateOutputsUnreadable, fmt.Sprintf(
			"Reading estate %q's output %q (%s): %s %s\n\nWhat the store said: %s.",
			other, name, key, kmsDenied.Headline(), kmsDenied.Remedy(), kmsDenied.Err))
	}
	var kmsUnusable *staterecord.KMSKeyUnusableError
	if errors.As(err, &kmsUnusable) {
		return tfdiags.Sourceless(tfdiags.Error, SummaryEstateOutputsUnreadable, fmt.Sprintf(
			"Reading estate %q's output %q (%s): %s %s\n\nWhat the store said: %s.",
			other, name, key, kmsUnusable.Headline(), kmsUnusable.Remedy(), kmsUnusable.Err))
	}
	if staterecord.IsAccessDenied(err) {
		at := key
		if namespace != "" {
			at = fmt.Sprintf("%s in namespace %q", key, namespace)
		}
		return tfdiags.Sourceless(tfdiags.Error, SummaryEstateOutputsDenied, fmt.Sprintf(
			"Estate %q's identity was refused reading estate %q's output %q, at %s. The data \"terraform_estate_outputs\" block declares that this estate reads %q's outputs, and the grant for that read is missing. %s\n\nWhat the store said: %s.",
			src.Estate, other, name, at, other, estateOutputsGrantRemedy(src, namespace, other, name), err))
	}
	return tfdiags.Sourceless(tfdiags.Error, SummaryEstateOutputsUnreadable, fmt.Sprintf(
		"Reading estate %q's output %q, at %s: %s.",
		other, name, key, err))
}

// estateOutputsGrantRemedy says how to add the missing grant, per backend.
func estateOutputsGrantRemedy(src EstateOutputsSource, namespace, other, name string) string {
	// A store that reads other estates only where declared is the cluster
	// one: its grant is a Role in the other estate's namespace.
	if src.OpenDeclared != nil {
		return kubernetesOutputsGrantRemedy(src.Estate, namespace, other, name)
	}
	switch src.StoreType {
	case "s3":
		bucket := src.Bucket
		if bucket == "" {
			bucket = "<bucket>"
		}
		return fmt.Sprintf(
			"Render this estate's bucket policy with the dependency declared, which grants s3:GetObject on %s* and nothing else of %q's:\n\n  examples/record-store-bucket/iam/render-policy.sh %s %s --reads-outputs-of %s",
			RootOutputKeyPrefix(other), other, src.Estate, bucket, other)
	case "local":
		return fmt.Sprintf("The record store is a local directory; give this run's user read access to %s under it.", RootOutputKeyPrefix(other))
	default:
		return fmt.Sprintf("Grant this run's identity read access to the keys under %s in the record store.", RootOutputKeyPrefix(other))
	}
}

// joinQuoted renders names as "a", "b" and "c".
func joinQuoted(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = fmt.Sprintf("%q", n)
	}
	if len(q) < 2 {
		return strings.Join(q, "")
	}
	return strings.Join(q[:len(q)-1], ", ") + " and " + q[len(q)-1]
}
