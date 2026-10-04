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
	"sync"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// This file is the Kubernetes half of GitHub issue #1371's cross-estate
// output read (estateoutputs.go), found missing by claim 44's Kubernetes
// cell (live/smoke/claims.json).
//
// On "s3" every estate sharing a bucket reads through one store, and the
// declaration that one estate reads another's outputs is the bucket policy
// (render-policy.sh --reads-outputs-of). On "kubernetes" that cannot work:
// each estate's records are in a namespace of their own by default, and the
// namespace IS the read isolation (KubernetesRecordNamespace), so the
// producer's tofu-outputs/<estate>/ keys are not in the reader's store at
// all; and in a namespace shared by configuration the reader's store refuses
// a record labelled as another estate's (staterecord.ForeignEstateRecordError,
// #1355), which is right for the reader's own records and wrong for this.
//
// So the declaration is the record_store block's: a
// `reads_outputs_of "<estate>"` block names the other estate and, when it is
// not the default, its namespace. A read of a declared estate opens a second
// store on that namespace, bound to the OTHER estate's name so its own
// foreign-label check holds the other way round, and wraps it so it can only
// Get. A read of an undeclared estate is refused by name before anything is
// sent. What the cluster's RBAC refuses is a denial naming the Role that
// grants exactly the read: get, on the named output Secrets, in that
// namespace.

// NewEstateOutputsSource is the consumer's side of a cross-estate output read
// for one run: store is the run's own record store (nil when it could not be
// opened, with unavailable saying why), rs the live block's record_store
// block (nil when there is none), and estate the reading estate.
func NewEstateOutputsSource(store staterecord.Store, rs *configs.LiveRecordStore, estate, unavailable string) *EstateOutputsSource {
	src := &EstateOutputsSource{
		Store:       store,
		Estate:      estate,
		Unavailable: unavailable,
	}
	if rs == nil {
		return src
	}
	src.StoreType = rs.Type
	src.Bucket = rs.Bucket
	if rs.Type == "kubernetes" {
		src.Declared = declaredOutputReads(rs)
		src.OpenDeclared = (&declaredKubernetesReads{rs: rs}).open
	}
	return src
}

// declaredOutputReads maps each reads_outputs_of block to the namespace it
// reads from: its own namespace argument, or the other estate's default.
func declaredOutputReads(rs *configs.LiveRecordStore) map[string]string {
	declared := make(map[string]string, len(rs.ReadsOutputsOf))
	for _, r := range rs.ReadsOutputsOf {
		ns := r.Namespace
		if !r.NamespaceSet {
			ns = KubernetesRecordNamespace(r.Estate)
		}
		declared[r.Estate] = ns
	}
	return declared
}

// declaredKubernetesReads opens, once per run and per declared estate, the
// read-only store a declared read goes through. It uses the same connection
// arguments as the run's own store, so the identity reading the other
// estate's outputs is this run's identity and the RBAC that answers is the
// cluster's own.
type declaredKubernetesReads struct {
	rs *configs.LiveRecordStore

	mu     sync.Mutex
	opened map[string]staterecord.Store
}

func (d *declaredKubernetesReads) open(_ context.Context, other, namespace string) (staterecord.Store, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cacheKey := other + "\x00" + namespace
	if s, ok := d.opened[cacheKey]; ok {
		return s, nil
	}
	// A default namespace is derived, and a derived name can fail where a
	// written one cannot (see kubernetesNamespaceFor): "tofu-records-" and
	// a long estate name is past a namespace's 63 characters.
	if errs := validation.IsDNS1123Label(namespace); len(errs) > 0 {
		return nil, fmt.Errorf("the default records namespace for estate %q is %q, which is not a Kubernetes namespace name (%s); set the reads_outputs_of block's namespace argument to the namespace %q keeps its records in",
			other, namespace, strings.Join(errs, "; "), other)
	}
	cfg, err := kubesweep.RestConfig(kubernetesAttrs(d.rs))
	if err != nil {
		return nil, fmt.Errorf("record_store \"kubernetes\": %w", err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("record_store \"kubernetes\": building a client: %w", err)
	}
	s, err := newDeclaredKubernetesReadStore(clientset, other, namespace)
	if err != nil {
		return nil, err
	}
	if d.opened == nil {
		d.opened = map[string]staterecord.Store{}
	}
	d.opened[cacheKey] = s
	return s, nil
}

// newDeclaredKubernetesReadStore is the store a declared read goes through:
// a [staterecord.KubernetesStore] on the other estate's namespace, opened AS
// that estate so a record labelled as its own is served and one labelled as
// anyone else's is still refused, and wrapped so nothing but Get reaches it.
// Split from [declaredKubernetesReads.open] so a test can hand it a fake
// cluster.
func newDeclaredKubernetesReadStore(clientset kubernetes.Interface, other, namespace string) (staterecord.Store, error) {
	ks, err := staterecord.NewKubernetesStore(staterecord.KubernetesConfig{
		Secrets: clientset.CoreV1().Secrets(namespace),
		// Used only to tell a missing namespace from a missing record on
		// a read that answered "nothing here"; see namespaceFault.
		Clientset: clientset,
		Namespace: namespace,
		KeyPrefix: backendKeyPrefix,
		Estate:    other,
	})
	if err != nil {
		return nil, fmt.Errorf("record_store \"kubernetes\": %w", err)
	}
	return readOnlyStore{get: ks.Get, what: fmt.Sprintf("estate %q's records in namespace %q", other, namespace)}, nil
}

// errReadOnlyStore is what every write to a declared read's store answers.
var errReadOnlyStore = errors.New("this store reads another estate's outputs and writes nothing")

// readOnlyStore is a [staterecord.Store] that answers Get and refuses
// everything else. A declared read is a read: nothing this estate does may
// write to, delete from or enumerate another estate's records namespace,
// whatever the identity running it happens to be allowed.
type readOnlyStore struct {
	get  func(ctx context.Context, key string) ([]byte, string, bool, error)
	what string
}

var _ staterecord.Store = readOnlyStore{}

func (s readOnlyStore) Get(ctx context.Context, key string) ([]byte, string, bool, error) {
	return s.get(ctx, key)
}

func (s readOnlyStore) PutIfVersion(context.Context, string, []byte, string) (string, error) {
	return "", fmt.Errorf("writing to %s: %w", s.what, errReadOnlyStore)
}

func (s readOnlyStore) PutIfAbsent(context.Context, string, []byte) (string, error) {
	return "", fmt.Errorf("writing to %s: %w", s.what, errReadOnlyStore)
}

func (s readOnlyStore) Delete(context.Context, string, string) error {
	return fmt.Errorf("deleting from %s: %w", s.what, errReadOnlyStore)
}

func (s readOnlyStore) List(context.Context, string) ([]string, error) {
	return nil, fmt.Errorf("listing %s: %w", s.what, errReadOnlyStore)
}

// kubernetesOutputsGrantRemedy is the Role and binding that grant estate
// reader a declared read of other's output name in namespace, and nothing
// else: get, on that output's Secret by name. No list, so no other record of
// other's is readable through it.
func kubernetesOutputsGrantRemedy(reader, namespace, other, name string) string {
	role := "tofu-reads-outputs-of-" + other
	secret := staterecord.KubernetesSecretName(RootOutputKey(other, name))
	return fmt.Sprintf(
		"Grant this run's identity get on estate %q's output Secrets in namespace %q, by name and with no other verb, so nothing else of %q's is readable:\n\n  kubectl -n %s create role %s --verb=get --resource=secrets --resource-name=%s\n  kubectl -n %s create rolebinding %s-%s --role=%s --serviceaccount=<namespace>:<name>\n\nwith one --resource-name per output the block reads (this one is output %q), and --user or --group in place of --serviceaccount for an identity that is not a ServiceAccount.",
		other, namespace, other,
		namespace, role, secret,
		namespace, role, reader, role,
		name)
}
