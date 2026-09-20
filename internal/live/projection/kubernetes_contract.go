// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"k8s.io/client-go/kubernetes"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// The cluster contract's half of the record store's open path. GitHub issue
// #1393; store.go holds the bucket's half and the sentinel handshake both
// share.

// ClusterContractFindings reads the cluster contract for the store a live
// block's record_store built, for one estate. ok is false when the store is
// not in a cluster and there is nothing to assert.
//
// It reports the cluster and knows nothing about waivers, for the reason
// [BucketContractFindings] gives: whether a run may proceed past a finding is
// the caller's question (#1340).
func ClusterContractFindings(ctx context.Context, store staterecord.Store, requiredVerbs []string) (findings []staterecord.ClusterFinding, ok bool, err error) {
	checker, ok := staterecord.AsClusterContractChecker(store)
	if !ok {
		return nil, false, nil
	}
	findings, err = checker.CheckClusterContract(ctx, staterecord.ClusterContractOptions{RequiredVerbs: requiredVerbs})
	return findings, true, err
}

// assertClusterOnFirstContact is one of the two places the cluster contract
// is asserted; internal/command's BeforeApply is the other, and an ordinary
// plan is deliberately neither. The ruling is #1339's, carried over unchanged
// because the reasoning is: the four properties are facts about the cluster,
// which do not change between two plans, and paying for four
// SelfSubjectAccessReviews and two policy reads on every plan buys nothing.
//
// # Why a read-only plan never reaches here
//
// This runs only when THIS run created the sentinel, and creating it needs
// create on Secrets. So the identity that gets here is one that may write,
// and asking it for all five of [staterecord.KubernetesRecordVerbs] is asking
// for what it has already demonstrated plus the two it has not. A plan under
// a get/list-only Role gets createdVersion == "" from
// [provisionStoreSentinel] and never arrives, which is #1370's reader
// tolerance and the reason the contract cannot refuse such a run for lacking
// create, update and delete. `choudoufu live-cluster -plan-identity` is how
// that identity asks the question on purpose.
//
// A refusal takes the sentinel back out, for [assertBucketOnFirstContact]'s
// reason: leaving it behind would make the second plan proceed against the
// cluster the first one refused.
func assertClusterOnFirstContact(ctx context.Context, store staterecord.Store, rs *configs.LiveRecordStore, estate, sentinelVersion string) error {
	findings, ok, err := ClusterContractFindings(ctx, store, staterecord.KubernetesRecordVerbs)
	if !ok {
		return nil
	}
	var refusal error
	if err != nil {
		refusal = err
	} else {
		// #1340: a waiver reaches only the settings it names. The warning a
		// waived run owes is internal/command's, which sees every run and
		// not just the first.
		// The warnings are internal/command's, which sees every apply; a
		// first contact has no channel for one and must not refuse on it.
		refused, _, _ := staterecord.SplitWaivedCluster(findings, rs.AllowInsecure)
		if msg := ClusterContractRefusalText(RecordNamespace(rs, estate), refused); msg != "" {
			refusal = &StoreRefusal{Err: errors.New(msg)}
		}
	}
	if refusal == nil {
		return nil
	}
	if delErr := store.Delete(ctx, SentinelKey(recordStoreKeyPrefix(rs, estate)), sentinelVersion); delErr != nil {
		return fmt.Errorf("%w\n\n(The store sentinel this run created could not be removed again: %s. The next plan will not repeat this check; the next apply will.)", refusal, delErr)
	}
	return refusal
}

// ClusterContractRefusalText renders every failed finding as one message,
// each under its own headline, or "" when all passed.
//
// Two or more refusals get one closing line naming them together. A plain
// kind cluster refuses an estate's first contact twice at once - no
// --encryption-provider-config and no estate boundary policy - and each
// refusal's own waiver line names only itself, so a reader following them
// both would write allow_insecure twice in one block, which is a duplicate
// argument and does not parse. The closing line is the one they can paste.
func ClusterContractRefusalText(namespace string, findings []staterecord.ClusterFinding) string {
	var b strings.Builder
	var refused []staterecord.ClusterSetting
	for _, f := range findings {
		summary, detail := staterecord.ClusterContractRefusal(namespace, f)
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
		fmt.Fprintf(&b, "\n\nTo accept all %d on purpose, the waiver is one line and not %d: `%s`",
			len(refused), len(refused), staterecord.ClusterWaiverArgument(refused...))
	}
	return b.String()
}

// RecordNamespace is the Kubernetes namespace a record_store block's records
// live in, for a message that has to name it. "" for a store that is not in a
// cluster.
func RecordNamespace(rs *configs.LiveRecordStore, estate string) string {
	if rs == nil || rs.Type != "kubernetes" {
		return ""
	}
	ns, err := kubernetesNamespaceFor(rs, estate)
	if err != nil {
		return ""
	}
	return ns
}

// VerifyCluster reads the cluster contract for a records namespace named
// directly, with no store opened and no sentinel written: it is what
// `choudoufu live-cluster` runs (GitHub issue #1393), the way
// [VerifyBucket] is what `choudoufu live-bucket` runs. The client is built
// exactly the way [NewRecordStore] builds the store's own, through
// [kubesweep.RestConfig].
//
// namespace may be "", in which case it is derived from estate the way the
// store derives it.
//
// requiredVerbs is what the caller says its runs need on Secrets. Nil takes
// [staterecord.KubernetesRecordVerbs]; a CI plan job passes
// [staterecord.KubernetesPlanVerbs].
func VerifyCluster(ctx context.Context, rs *configs.LiveRecordStore, estate, namespace string, requiredVerbs []string) (findings []staterecord.ClusterFinding, ns string, err error) {
	if rs == nil {
		rs = &configs.LiveRecordStore{Type: "kubernetes"}
	}
	ns = namespace
	if ns == "" {
		ns, err = kubernetesNamespaceFor(rs, estate)
		if err != nil {
			return nil, "", err
		}
	}
	cfg, err := kubesweep.RestConfig(kubernetesAttrs(rs))
	if err != nil {
		return nil, ns, fmt.Errorf("reaching the cluster: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, ns, fmt.Errorf("building a client: %w", err)
	}
	findings, err = staterecord.CheckClusterContract(ctx, clientset, staterecord.ClusterContractOptions{
		Namespace:     ns,
		Estate:        estate,
		RequiredVerbs: requiredVerbs,
		// Nothing has used this namespace, so its existence is the report's
		// to establish, and an absent one is reported in the store's own
		// words.
		NamespaceKnownToExist: false,
	})
	return findings, ns, err
}
