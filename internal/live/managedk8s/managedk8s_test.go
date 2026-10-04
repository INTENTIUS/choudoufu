// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package managedk8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	container "google.golang.org/api/container/v1"

	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// These hold each provider's reading to the fields that provider documents,
// over an httptest server answering in that provider's own response shape.
// What they cannot measure is whether the real API still answers in that
// shape: live/managed-k8s/harness.sh is that measurement, and it is the
// maintainer's to run (GitHub issue #1524).

func staticEKS(_ context.Context, region string) (eksAuth, error) {
	if region == "" {
		region = "us-east-1"
	}
	return eksAuth{creds: aws.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}, region: region}, nil
}

func eksServer(t *testing.T, status int, errType string, body any) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		if errType != "" {
			w.Header().Set("X-Amzn-Errortype", errType+":http://internal.amazon.com/coral/com.amazonaws.eks/")
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestEKSReadsTheCustomerKey(t *testing.T) {
	body := map[string]any{"cluster": map[string]any{
		"name": "prod", "arn": "arn:aws:eks:us-east-1:111122223333:cluster/prod",
		"version": "1.27", "endpoint": "https://ABC.gr7.us-east-1.eks.amazonaws.com",
		"encryptionConfig": []any{map[string]any{
			"resources": []string{"secrets"},
			"provider":  map[string]any{"keyArn": "arn:aws:kms:us-east-1:111122223333:key/k"},
		}},
	}}
	srv, seen := eksServer(t, 200, "", body)
	r := &Reader{EKSEndpoint: srv.URL, eksCredentials: staticEKS}
	got, err := r.SecretsEncryption(context.Background(), staterecord.ManagedControlPlane{Provider: "eks", Name: "prod", Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Verdict != staterecord.EncryptionOn || !strings.Contains(got.Detail, "key/k") {
		t.Fatalf("got %+v, want encrypted with the named key", got)
	}
	if len(got.Endpoints) != 1 || got.Endpoints[0] != "https://ABC.gr7.us-east-1.eks.amazonaws.com" {
		t.Errorf("endpoints %v, want the cluster's own", got.Endpoints)
	}
	if len(*seen) != 1 {
		t.Fatalf("%d requests, want one DescribeCluster", len(*seen))
	}
	req := (*seen)[0]
	if req.Method != http.MethodGet || req.URL.Path != "/clusters/prod" {
		t.Errorf("asked %s %s, want GET /clusters/prod", req.Method, req.URL.Path)
	}
	if a := req.Header.Get("Authorization"); !strings.HasPrefix(a, "AWS4-HMAC-SHA256 ") || !strings.Contains(a, "/us-east-1/eks/aws4_request") {
		t.Errorf("Authorization %q is not a SigV4 signature for eks in us-east-1", a)
	}
}

func TestEKSDefaultEncryption(t *testing.T) {
	for _, c := range []struct {
		version string
		want    staterecord.EncryptionVerdict
	}{
		{"1.27", staterecord.EncryptionOff},
		{"1.28", staterecord.EncryptionOn},
		{"1.31", staterecord.EncryptionOn},
		{"", staterecord.EncryptionUndetermined},
		{"banana", staterecord.EncryptionUndetermined},
	} {
		var d eksDescribeResponse
		d.Cluster.Version = c.version
		d.Cluster.Endpoint = "https://x"
		got := eksEncryption(d)
		if got.Verdict != c.want {
			t.Errorf("version %q with no encryptionConfig: verdict %v, want %v (%s)", c.version, got.Verdict, c.want, got.Detail)
		}
		if got.Detail == "" {
			t.Errorf("version %q: no detail", c.version)
		}
	}
}

// A key for some other resource is not a key for secrets.
func TestEKSKeyForOtherResourcesIsNotSecrets(t *testing.T) {
	var d eksDescribeResponse
	d.Cluster.Version = "1.27"
	d.Cluster.EncryptionConfig = append(d.Cluster.EncryptionConfig, struct {
		Resources []string `json:"resources"`
		Provider  struct {
			KeyArn string `json:"keyArn"`
		} `json:"provider"`
	}{Resources: []string{"configmaps"}})
	d.Cluster.EncryptionConfig[0].Provider.KeyArn = "arn:aws:kms:us-east-1:1:key/other"
	if got := eksEncryption(d); got.Verdict != staterecord.EncryptionOff {
		t.Fatalf("a key covering configmaps only read as %v", got.Verdict)
	}
}

func TestEKSErrors(t *testing.T) {
	for _, c := range []struct {
		status  int
		errType string
		check   func(error) bool
	}{
		{403, "AccessDeniedException", func(err error) bool {
			var d *staterecord.ControlPlaneDeniedError
			return errors.As(err, &d) && d.Action == eksDescribeAction
		}},
		{404, "ResourceNotFoundException", func(err error) bool { var n *staterecord.ControlPlaneNotFoundError; return errors.As(err, &n) }},
		{403, "UnrecognizedClientException", func(err error) bool {
			var d *staterecord.ControlPlaneDeniedError
			return err != nil && !errors.As(err, &d)
		}},
		{500, "ServerException", func(err error) bool { return err != nil }},
	} {
		srv, _ := eksServer(t, c.status, c.errType, map[string]string{"message": "nope"})
		r := &Reader{EKSEndpoint: srv.URL, eksCredentials: staticEKS}
		_, err := r.SecretsEncryption(context.Background(), staterecord.ManagedControlPlane{Provider: "eks", Name: "prod", Region: "us-east-1"})
		if !c.check(err) {
			t.Errorf("%d %s: got %v (%T)", c.status, c.errType, err, err)
		}
	}
}

func TestKubeMinor(t *testing.T) {
	for in, want := range map[string][2]int{"1.30": {1, 30}, "v1.28.3": {1, 28}, "1.29+": {1, 29}} {
		maj, mi, ok := kubeMinor(in)
		if !ok || maj != want[0] || mi != want[1] {
			t.Errorf("kubeMinor(%q) = %d.%d %v", in, maj, mi, ok)
		}
	}
	if _, _, ok := kubeMinor("1"); ok {
		t.Error(`kubeMinor("1") read a minor that is not there`)
	}
}

func TestGKEReadsDatabaseEncryption(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "prod", "selfLink": "https://container.googleapis.com/v1/projects/acme/locations/europe-west1/clusters/prod",
			"endpoint": "34.1.2.3",
			"databaseEncryption": map[string]any{
				"state": "ENCRYPTED", "currentState": "CURRENT_STATE_ENCRYPTED",
				"keyName": "projects/acme/locations/europe-west1/keyRings/r/cryptoKeys/k",
			},
			"controlPlaneEndpointsConfig": map[string]any{"dnsEndpointConfig": map[string]any{"endpoint": "gke-abc.europe-west1.gke.goog"}},
		})
	}))
	defer srv.Close()
	r := &Reader{GKEEndpoint: srv.URL + "/", HTTPClient: srv.Client(), gkeNoAuth: true}
	got, err := r.SecretsEncryption(context.Background(), staterecord.ManagedControlPlane{Provider: "gke", Name: "prod", Project: "acme", Location: "europe-west1"})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/projects/acme/locations/europe-west1/clusters/prod" {
		t.Errorf("asked %s", path)
	}
	if got.Verdict != staterecord.EncryptionOn || !strings.Contains(got.Detail, "cryptoKeys/k") {
		t.Fatalf("got %+v", got)
	}
	if strings.Join(got.Endpoints, ",") != "34.1.2.3,gke-abc.europe-west1.gke.goog" {
		t.Errorf("endpoints %v", got.Endpoints)
	}
}

func TestGKEErrors(t *testing.T) {
	for _, c := range []struct {
		status int
		check  func(error) bool
	}{
		{403, func(err error) bool {
			var d *staterecord.ControlPlaneDeniedError
			return errors.As(err, &d) && d.Action == gkeGetPermission
		}},
		{404, func(err error) bool { var n *staterecord.ControlPlaneNotFoundError; return errors.As(err, &n) }},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(c.status)
			_, _ = fmt.Fprintf(w, `{"error":{"code":%d,"message":"nope"}}`, c.status)
		}))
		r := &Reader{GKEEndpoint: srv.URL + "/", HTTPClient: srv.Client(), gkeNoAuth: true}
		_, err := r.SecretsEncryption(context.Background(), staterecord.ManagedControlPlane{Provider: "gke", Name: "prod", Project: "acme", Location: "europe-west1"})
		srv.Close()
		if !c.check(err) {
			t.Errorf("HTTP %d: got %v (%T)", c.status, err, err)
		}
	}
}

func TestGKEEncryptionStates(t *testing.T) {
	for _, c := range []struct {
		de   *container.DatabaseEncryption
		want staterecord.EncryptionVerdict
	}{
		{nil, staterecord.EncryptionOff},
		{&container.DatabaseEncryption{State: "DECRYPTED", CurrentState: "CURRENT_STATE_DECRYPTED"}, staterecord.EncryptionOff},
		{&container.DatabaseEncryption{State: "DECRYPTED", CurrentState: "CURRENT_STATE_DECRYPTION_PENDING"}, staterecord.EncryptionOff},
		{&container.DatabaseEncryption{State: "ENCRYPTED", CurrentState: "CURRENT_STATE_ENCRYPTED", KeyName: "k"}, staterecord.EncryptionOn},
		{&container.DatabaseEncryption{State: "ALL_OBJECTS_ENCRYPTION_ENABLED", CurrentState: "CURRENT_STATE_ALL_OBJECTS_ENCRYPTION_ENABLED", KeyName: "k"}, staterecord.EncryptionOn},
		// Asked for and not yet in force: the desired state is not the answer.
		{&container.DatabaseEncryption{State: "ENCRYPTED", CurrentState: "CURRENT_STATE_ENCRYPTION_PENDING", KeyName: "k"}, staterecord.EncryptionUndetermined},
		{&container.DatabaseEncryption{State: "ENCRYPTED", CurrentState: "CURRENT_STATE_ENCRYPTION_ERROR", KeyName: "k"}, staterecord.EncryptionUndetermined},
		{&container.DatabaseEncryption{State: "ENCRYPTED", KeyName: "k"}, staterecord.EncryptionOn},
		{&container.DatabaseEncryption{State: "DECRYPTED"}, staterecord.EncryptionOff},
		{&container.DatabaseEncryption{State: "UNKNOWN"}, staterecord.EncryptionUndetermined},
	} {
		got := gkeEncryption(&container.Cluster{Name: "prod", DatabaseEncryption: c.de})
		if got.Verdict != c.want {
			t.Errorf("%+v: verdict %v, want %v (%s)", c.de, got.Verdict, c.want, got.Detail)
		}
	}
}

func aksServer(t *testing.T, status int, body string) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func staticAzure(context.Context) (string, error) { return "tok", nil }

func TestAKSReadsKMS(t *testing.T) {
	srv, seen := aksServer(t, 200, `{"id":"/subscriptions/s/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/prod","name":"prod",
"properties":{"kubernetesVersion":"1.30.3","fqdn":"prod-dns-1.hcp.westeurope.azmk8s.io",
"securityProfile":{"azureKeyVaultKms":{"enabled":true,"keyId":"https://v.vault.azure.net/keys/k/1","keyVaultNetworkAccess":"Public"}}}}`)
	r := &Reader{AKSEndpoint: srv.URL, aksToken: staticAzure}
	got, err := r.SecretsEncryption(context.Background(), staterecord.ManagedControlPlane{Provider: "aks", Name: "prod", ResourceGroup: "rg", SubscriptionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Verdict != staterecord.EncryptionOn || !strings.Contains(got.Detail, "keys/k/1") {
		t.Fatalf("got %+v", got)
	}
	req := (*seen)[0]
	if req.URL.Path != "/subscriptions/s/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/prod" || req.URL.Query().Get("api-version") != aksAPIVersion {
		t.Errorf("asked %s", req.URL)
	}
	if req.Header.Get("Authorization") != "Bearer tok" {
		t.Errorf("Authorization %q", req.Header.Get("Authorization"))
	}
	if len(got.Endpoints) != 1 || got.Endpoints[0] != "prod-dns-1.hcp.westeurope.azmk8s.io" {
		t.Errorf("endpoints %v", got.Endpoints)
	}
}

func TestAKSWithoutKMSFails(t *testing.T) {
	for _, body := range []string{
		`{"properties":{"fqdn":"x"}}`,
		`{"properties":{"fqdn":"x","securityProfile":{}}}`,
		`{"properties":{"fqdn":"x","securityProfile":{"azureKeyVaultKms":{"enabled":false}}}}`,
	} {
		var mc aksManagedCluster
		if err := json.Unmarshal([]byte(body), &mc); err != nil {
			t.Fatal(err)
		}
		if got := aksEncryption(mc); got.Verdict != staterecord.EncryptionOff {
			t.Errorf("%s: verdict %v, want off", body, got.Verdict)
		}
	}
}

func TestAKSErrors(t *testing.T) {
	srv, _ := aksServer(t, 403, `{"error":{"code":"AuthorizationFailed","message":"no"}}`)
	r := &Reader{AKSEndpoint: srv.URL, aksToken: staticAzure}
	_, err := r.SecretsEncryption(context.Background(), staterecord.ManagedControlPlane{Provider: "aks", Name: "prod", ResourceGroup: "rg", SubscriptionID: "s"})
	var d *staterecord.ControlPlaneDeniedError
	if !errors.As(err, &d) || d.Action != aksReadAction {
		t.Errorf("403: got %v", err)
	}
	srv2, _ := aksServer(t, 404, `{"error":{"code":"ResourceNotFound","message":"no"}}`)
	r2 := &Reader{AKSEndpoint: srv2.URL, aksToken: staticAzure}
	_, err = r2.SecretsEncryption(context.Background(), staterecord.ManagedControlPlane{Provider: "aks", Name: "prod", ResourceGroup: "rg", SubscriptionID: "s"})
	var n *staterecord.ControlPlaneNotFoundError
	if !errors.As(err, &n) {
		t.Errorf("404: got %v", err)
	}
}

func TestUnknownProviderIsAnError(t *testing.T) {
	if _, err := (&Reader{}).SecretsEncryption(context.Background(), staterecord.ManagedControlPlane{Provider: "doks"}); err == nil {
		t.Fatal("an unknown provider was answered")
	}
}

func TestControlPlaneFor(t *testing.T) {
	const host = "https://ABC.gr7.eu-west-2.eks.amazonaws.com"
	for _, c := range []struct {
		name     string
		declared *staterecord.ManagedControlPlane
		host     string
		cmd      string
		args     []string
		want     *staterecord.ManagedControlPlane
	}{
		{name: "aws eks get-token", host: host, cmd: "aws", args: []string{"--region", "eu-west-2", "eks", "get-token", "--cluster-name", "prod", "--output", "json"},
			want: &staterecord.ManagedControlPlane{Provider: "eks", Name: "prod", Region: "eu-west-2"}},
		{name: "region from the host", host: host, cmd: "/usr/local/bin/aws", args: []string{"eks", "get-token", "--cluster-name=prod"},
			want: &staterecord.ManagedControlPlane{Provider: "eks", Name: "prod", Region: "eu-west-2"}},
		{name: "aws-iam-authenticator", host: host, cmd: "aws-iam-authenticator", args: []string{"token", "-i", "prod"},
			want: &staterecord.ManagedControlPlane{Provider: "eks", Name: "prod", Region: "eu-west-2"}},
		{name: "not an EKS host", host: "https://127.0.0.1:6443", cmd: "aws", args: []string{"eks", "get-token", "--cluster-name", "prod"}},
		{name: "an EKS host and no plugin naming it", host: host},
		{name: "aws but not get-token", host: host, cmd: "aws", args: []string{"sts", "get-caller-identity"}},
		{name: "gke plugin is not inferred", host: "https://34.1.2.3", cmd: "gke-gcloud-auth-plugin"},
		{name: "declared wins", host: host, cmd: "aws", args: []string{"eks", "get-token", "--cluster-name", "other"},
			declared: &staterecord.ManagedControlPlane{Provider: "eks", Name: "prod"},
			want:     &staterecord.ManagedControlPlane{Provider: "eks", Name: "prod", Region: "eu-west-2"}},
		{name: "declared gke", host: "https://34.1.2.3",
			declared: &staterecord.ManagedControlPlane{Provider: "gke", Name: "prod", Project: "p", Location: "l"},
			want:     &staterecord.ManagedControlPlane{Provider: "gke", Name: "prod", Project: "p", Location: "l"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := ControlPlaneFor(c.declared, c.host, c.cmd, c.args)
			if c.want == nil {
				if got != nil {
					t.Fatalf("got %+v, want none", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("got none, want %+v", c.want)
			}
			if got.Source == "" {
				t.Errorf("no Source on %+v", got)
			}
			got.Source = ""
			if *got != *c.want {
				t.Errorf("got %+v, want %+v", *got, *c.want)
			}
		})
	}
}
