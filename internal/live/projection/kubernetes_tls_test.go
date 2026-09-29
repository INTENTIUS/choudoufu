// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// GitHub issue #1448, section C: `insecure = true` on the record_store
// "kubernetes" block is the tls_verification finding. staterecord's tests
// hold the finding itself; these hold the two things only this package can
// get wrong, which are that the block's argument reaches the contract on
// both paths that build a client from it, and that a first contact refuses
// on it and takes its sentinel back out like any other refusal.

// openInsecureCluster is [openCluster] for a store whose block set
// `insecure = true`.
func openInsecureCluster(t *testing.T, cs *clusterFake, rs *configs.LiveRecordStore) error {
	t.Helper()
	store, err := staterecord.NewKubernetesStore(staterecord.KubernetesConfig{
		Secrets:     cs.CoreV1().Secrets(contractNamespace),
		Clientset:   cs,
		Namespace:   contractNamespace,
		Estate:      contractEstate,
		InsecureTLS: true,
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %v", err)
	}
	_, err = openBuiltStore(context.Background(), store, rs, contractEstate)
	return err
}

func TestInsecureTLSRefusesFirstContactByName(t *testing.T) {
	cs := newClusterFake(t)
	rs := kubernetesRecordStore()
	rs.Kubernetes.Insecure = true

	err := openInsecureCluster(t, cs, rs)
	if err == nil {
		t.Fatal("first contact over a connection with verification turned off was not refused")
	}
	if !IsStoreRefusal(err) {
		t.Errorf("the refusal is an outage, so live-plan would carry on past it (#1376): %v", err)
	}
	for _, want := range []string{"tls_verification", "`insecure = true`", `allow_insecure = ["tls_verification"]`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %s: %v", want, err)
		}
	}
	secrets, listErr := cs.CoreV1().Secrets(contractNamespace).List(context.Background(), metav1.ListOptions{})
	if listErr != nil {
		t.Fatalf("listing the records namespace: %v", listErr)
	}
	if len(secrets.Items) != 0 {
		t.Errorf("the refused first contact left %d Secret(s) behind", len(secrets.Items))
	}

	// A waiver of something else does not reach it.
	if err := openInsecureCluster(t, newClusterFake(t), kubernetesRecordStore("encryption_at_rest")); err == nil {
		t.Error("waiving encryption_at_rest also waived tls_verification")
	}
}

func TestInsecureTLSWaivedLetsFirstContactProceed(t *testing.T) {
	if err := openInsecureCluster(t, newClusterFake(t), kubernetesRecordStore("tls_verification")); err != nil {
		t.Fatalf("a run that waives tls_verification was still refused: %v", err)
	}
}

// scopedAPIServer is an API server behind a self-signed certificate that
// answers yes to every SelfSubjectAccessReview and 403 to everything else,
// which is how a real one answers an identity scoped to its own namespace:
// every finding but namespace_access comes out NOT CHECKED, and none of them
// is an error.
func scopedAPIServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/selfsubjectaccessreviews") {
			// The request body is protobuf, which client-go prefers for the
			// built-in types, so it is not read: the answer is yes whatever
			// was asked.
			review := map[string]any{
				"kind": "SelfSubjectAccessReview", "apiVersion": "authorization.k8s.io/v1",
				"status": map[string]any{"allowed": true},
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(review)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind": "Status", "apiVersion": "v1", "status": "Failure",
			"reason": "Forbidden", "code": http.StatusForbidden, "message": "forbidden",
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func blockFor(t *testing.T, srv *httptest.Server) *configs.LiveRecordStore {
	t.Helper()
	t.Setenv("KUBECONFIG", "")
	t.Setenv("KUBE_CONFIG_PATH", "")
	t.Setenv("KUBE_CONFIG_PATHS", "")
	rs := kubernetesRecordStore()
	rs.Kubernetes.Host = srv.URL
	rs.Kubernetes.Token = "not-a-real-token"
	return rs
}

func hasTLSFinding(findings []staterecord.Finding) bool {
	for _, f := range findings {
		if f.Setting == staterecord.ClusterTLSVerification {
			return true
		}
	}
	return false
}

// TestVerifyClusterCarriesTheBlocksInsecure is `choudoufu live-cluster`'s
// path. The same server is reached twice: once with `insecure = true`, which
// has to report the finding, and once with the server's certificate named as
// the CA, which has to report nothing about TLS.
func TestVerifyClusterCarriesTheBlocksInsecure(t *testing.T) {
	srv := scopedAPIServer(t)

	insecure := blockFor(t, srv)
	insecure.Kubernetes.Insecure = true
	findings, _, err := VerifyCluster(context.Background(), insecure, contractEstate, "", nil)
	if err != nil {
		t.Fatalf("VerifyCluster with insecure = true: %v", err)
	}
	if !hasTLSFinding(findings) {
		t.Errorf("the block sets insecure = true and live-cluster's findings do not say so: %+v", findings)
	}
	if findings[0].Setting != staterecord.ClusterTLSVerification || findings[0].OK() {
		t.Errorf("the first finding is %q (ok=%v), want a failed tls_verification", findings[0].Setting, findings[0].OK())
	}

	verified := blockFor(t, srv)
	verified.Kubernetes.ClusterCACertificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	findings, _, err = VerifyCluster(context.Background(), verified, contractEstate, "", nil)
	if err != nil {
		t.Fatalf("VerifyCluster with the server's CA: %v", err)
	}
	if hasTLSFinding(findings) {
		t.Errorf("a verified connection carries a tls_verification finding: %+v", findings)
	}
}

// TestNewKubernetesStoreCarriesTheBlocksInsecure is the run's path: the store
// the block builds is what the first-contact assertion and BeforeApply ask.
func TestNewKubernetesStoreCarriesTheBlocksInsecure(t *testing.T) {
	srv := scopedAPIServer(t)

	for _, tc := range []struct {
		name string
		set  func(*configs.LiveRecordStore)
		want bool
	}{
		{"insecure = true", func(rs *configs.LiveRecordStore) { rs.Kubernetes.Insecure = true }, true},
		{"the server's CA", func(rs *configs.LiveRecordStore) {
			rs.Kubernetes.ClusterCACertificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs := blockFor(t, srv)
			tc.set(rs)
			store, err := newKubernetesStore(rs, contractEstate)
			if err != nil {
				t.Fatalf("newKubernetesStore: %v", err)
			}
			findings, checker, err := ContractFindings(context.Background(), store, rs, contractEstate)
			if err != nil || checker == nil {
				t.Fatalf("ContractFindings: checker=%v err=%v", checker, err)
			}
			if got := hasTLSFinding(findings); got != tc.want {
				t.Errorf("tls_verification finding present = %v, want %v: %+v", got, tc.want, findings)
			}
		})
	}
}
