// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// GitHub issue #1448 section C, measured end to end against a real API
// server: the audit's scenario, which is the only place the defect is
// visible. The unit tests either side of this one drive the classification
// and the open path over fakes; what a fake cannot do is run an admission
// policy, and the whole finding is that an admission denial and the
// authorizer's are the same 403 from the client's side.
//
// # Running it
//
//	kind create cluster --name <name> --kubeconfig /tmp/kc
//	kubectl --kubeconfig /tmp/kc apply -f live/kubernetes/estate-boundary.yaml
//	kubectl --kubeconfig /tmp/kc create namespace tofu-records-alice
//	# a ServiceAccount with get/list/create/update/delete on secrets in that
//	# namespace and NO `use` on estates.choudoufu.intentius.io/alice, and one
//	# with get and list only:
//	CHOUDOUFU_K8S_RECORD_KUBECONFIG=/tmp/kc \
//	CHOUDOUFU_K8S_RECORD_NAMESPACE=tofu-records-alice \
//	CHOUDOUFU_K8S_FENCE_ESTATE=alice \
//	CHOUDOUFU_K8S_FENCED_USER=system:serviceaccount:default:fenced \
//	CHOUDOUFU_K8S_READER_USER=system:serviceaccount:default:reader \
//	go test ./internal/live/projection/ -run TestFencedIdentityOnACluster -v
//
// The kubeconfig's own identity has to be able to impersonate, which kind's
// admin context is. A skip here is not a pass: the skip names the fixture
// that is missing.

const (
	fenceKubeconfigEnvVar = "CHOUDOUFU_K8S_RECORD_KUBECONFIG"
	fenceNamespaceEnvVar  = "CHOUDOUFU_K8S_RECORD_NAMESPACE"
	fenceEstateEnvVar     = "CHOUDOUFU_K8S_FENCE_ESTATE"
	fencedUserEnvVar      = "CHOUDOUFU_K8S_FENCED_USER"
	readerUserEnvVar      = "CHOUDOUFU_K8S_READER_USER"
)

type fenceFixtures struct {
	base      *rest.Config
	admin     kubernetes.Interface
	namespace string
	estate    string
	fenced    string
	reader    string
}

// fenceEnv reads the fixtures, or skips naming the one that is absent.
func fenceEnv(t *testing.T) fenceFixtures {
	t.Helper()
	need := func(name string) string {
		v := strings.TrimSpace(os.Getenv(name))
		if v == "" {
			t.Skipf("%s is not set. This test needs a real API server running live/kubernetes/estate-boundary.yaml, a records namespace, and two ServiceAccounts: one with RBAC on Secrets and no estate grant (%s), one with get and list only (%s). See this file's header for the commands. A skip here is not a pass.", name, fencedUserEnvVar, readerUserEnvVar)
		}
		return v
	}
	path := need(fenceKubeconfigEnvVar)
	f := fenceFixtures{
		namespace: need(fenceNamespaceEnvVar),
		estate:    need(fenceEstateEnvVar),
		fenced:    need(fencedUserEnvVar),
		reader:    need(readerUserEnvVar),
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		t.Fatalf("reading the kubeconfig at %s: %v", path, err)
	}
	f.base = cfg
	f.admin, err = kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("building a client: %v", err)
	}
	ctx := context.Background()
	if _, err := f.admin.AdmissionregistrationV1().ValidatingAdmissionPolicies().Get(ctx, staterecord.EstateBoundaryPolicyName, metav1.GetOptions{}); err != nil {
		t.Skipf("the %q ValidatingAdmissionPolicy is not installed on this cluster (%v), so nothing here would be fenced and an allow would prove nothing. `kubectl apply -f live/kubernetes/estate-boundary.yaml` installs it. A skip here is not a pass.", staterecord.EstateBoundaryPolicyName, err)
	}
	if _, err := f.admin.CoreV1().Namespaces().Get(ctx, f.namespace, metav1.GetOptions{}); err != nil {
		t.Fatalf("namespace %q: %v. The caller creates it; the store does not.", f.namespace, err)
	}
	return f
}

// storeAs builds the record store an ordinary run would build, under an
// impersonated identity.
func (f fenceFixtures) storeAs(t *testing.T, user string) staterecord.Store {
	t.Helper()
	cfg := rest.CopyConfig(f.base)
	if user != "" {
		cfg.Impersonate = rest.ImpersonationConfig{UserName: user}
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("building a client for %q: %v", user, err)
	}
	store, err := staterecord.NewKubernetesStore(staterecord.KubernetesConfig{
		Secrets:   cs.CoreV1().Secrets(f.namespace),
		Clientset: cs,
		Namespace: f.namespace,
		Estate:    f.estate,
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %v", err)
	}
	return store
}

// TestFencedIdentityOnACluster is the audit's scenario. An earlier granted run
// provisions the sentinel; the fenced identity then opens the store, and used
// to open it green because the estate boundary policy's 403 was read as
// #1370's reader tolerance. The read-only identity opens it still, which is
// the half that must not be lost.
func TestFencedIdentityOnACluster(t *testing.T) {
	f := fenceEnv(t)
	ctx := context.Background()

	var seg [8]byte
	if _, err := rand.Read(seg[:]); err != nil {
		t.Fatalf("generating a key segment: %v", err)
	}
	prefix := "fence-" + hex.EncodeToString(seg[:])
	rs := &configs.LiveRecordStore{Type: "kubernetes", KeyPrefix: prefix, KeyPrefixSet: true}
	sentinel := SentinelKey(RecordStoreKeyPrefix(rs, f.estate))

	admin := f.storeAs(t, "")
	t.Cleanup(func() {
		_, version, exists, err := admin.Get(context.Background(), sentinel)
		if err == nil && exists {
			if err := admin.Delete(context.Background(), sentinel, version); err != nil {
				t.Errorf("removing the sentinel this test wrote: %v", err)
			}
		}
	})

	// The fence proven red before any allow here is trusted. The policy is
	// installed, but an objectSelector, a binding scoped elsewhere or a
	// cluster-admin wildcard would each make it allow this write, and then
	// every "refused" below would be refused for another reason.
	t.Run("the fence refuses the fenced identity", func(t *testing.T) {
		cfg := rest.CopyConfig(f.base)
		cfg.Impersonate = rest.ImpersonationConfig{UserName: f.fenced}
		cs, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			t.Fatalf("building a client: %v", err)
		}
		_, err = cs.CoreV1().Secrets(f.namespace).Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "tofu-record-fence-probe-" + hex.EncodeToString(seg[:]),
				Labels: map[string]string{staterecord.KubernetesEstateLabel: f.estate},
			},
		}, metav1.CreateOptions{})
		if err == nil {
			t.Fatalf("%s created a Secret labelled for estate %q, so the estate boundary policy is not fencing this namespace and nothing below would measure anything", f.fenced, f.estate)
		}
		if !k8serrors.IsForbidden(err) {
			t.Fatalf("the write failed with something other than a 403, so the policy is not what refused it: %v", err)
		}
		if !strings.Contains(err.Error(), staterecord.EstateBoundaryPolicyName) {
			t.Fatalf("the 403 does not name the estate boundary policy, so something else refused this write: %v", err)
		}
		t.Logf("the fence is live: %v", err)
	})

	// An earlier legitimate run, under an identity that holds the estate.
	if _, err := admin.PutIfAbsent(ctx, sentinel, []byte(sentinelPayload)); err != nil {
		t.Fatalf("provisioning the sentinel as an identity that holds the estate: %v", err)
	}

	t.Run("the fenced identity is refused by name", func(t *testing.T) {
		_, err := openBuiltStore(ctx, f.storeAs(t, f.fenced), rs, f.estate)
		if err == nil {
			t.Fatal("the store opened for an identity every record write of which the estate boundary policy refuses; the contract is skipped and the apply fails on its first write")
		}
		if !IsStoreRefusal(err) {
			t.Errorf("the refusal is reported as an outage, so live-plan and live-mv would carry on: %v", err)
		}
		for _, want := range []string{
			staterecord.EstateBoundaryPolicyName,
			"estates.choudoufu.intentius.io/" + f.estate,
			"live/kubernetes/estate-grant.yaml",
			f.fenced,
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not say %q:\n%s", want, err)
			}
		}
		t.Logf("refused: %v", err)
	})

	t.Run("the read-only identity still opens the store", func(t *testing.T) {
		opened, err := openBuiltStore(ctx, f.storeAs(t, f.reader), rs, f.estate)
		if err != nil {
			t.Fatalf("#1370's read-only identity can no longer plan against a store an earlier run provisioned: %v", err)
		}
		if opened == nil {
			t.Fatal("openBuiltStore returned no store and no error")
		}
		keys, err := opened.List(ctx, staterecord.NamespacePrefix(RecordStoreKeyPrefix(rs, f.estate)))
		if err != nil {
			t.Fatalf("the opened store cannot list: %v", err)
		}
		if len(keys) == 0 {
			t.Error("the opened store lists nothing, so the sentinel this test wrote is not visible to it")
		}
	})
}
