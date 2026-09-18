// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GitHub issue #1114. Every one of these drives a real
// [restclient.Config] through a real client-go client at a real HTTPS
// server, because the thing under test is whether client-go runs a
// program, and nothing short of the program running proves it.
//
// The server has to be TLS. client-go reads the kubeconfig's user
// credentials only when restclient.IsConfigTransportTLS holds
// (tools/clientcmd/client_config.go), so a plain-HTTP httptest.Server
// silently drops every credential and the exec plugin is never
// consulted - which is exactly the false negative the first measurement
// of this issue produced.

// execPlugin writes a credential plugin into dir and returns its path
// and the path of the sentinel file it touches when it runs. body is the
// shell after the sentinel, which prints the plugin's answer.
func execPlugin(t *testing.T, body string) (cmd, sentinel string) {
	t.Helper()
	// A directory per plugin: client-go's exec authenticator has a
	// process-global cache keyed by the command and its arguments, so
	// two tests naming the same path would share one authenticator and
	// the second would never run the program.
	return execPluginIn(t, t.TempDir(), body)
}

func execPluginIn(t *testing.T, dir, body string) (cmd, sentinel string) {
	t.Helper()
	cmd = filepath.Join(dir, "plugin.sh")
	sentinel = filepath.Join(dir, "ran")
	script := "#!/bin/sh\ntouch " + sentinel + "\n" + body
	if err := os.WriteFile(cmd, []byte(script), 0o700); err != nil {
		t.Fatalf("writing plugin: %v", err)
	}
	return cmd, sentinel
}

// tokenPlugin prints a valid ExecCredential carrying token, and records
// its argv and two environment variables into a file beside it.
func tokenPlugin(t *testing.T, apiVersion, token string) (cmd, sentinel, record string) {
	t.Helper()
	dir := t.TempDir()
	record = filepath.Join(dir, "record")
	body := fmt.Sprintf("printf '%%s\\n' \"$@\" > %s\n"+
		"printf 'CLUSTER=%%s\\nPROFILE=%%s\\n' \"$EKS_CLUSTER\" \"$AWS_PROFILE\" >> %s\n"+
		"cat <<'J'\n{\"apiVersion\":%q,\"kind\":\"ExecCredential\",\"status\":{\"token\":%q}}\nJ\n",
		record, record, apiVersion, token)
	cmd, sentinel = execPluginIn(t, dir, body)
	return cmd, sentinel, record
}

// apiServer is an HTTPS server that answers GET /api/v1 with status and
// body, and records the Authorization header of every request.
type apiServer struct {
	*httptest.Server
	auth     string
	requests int
}

func newAPIServer(t *testing.T, status int, body string) *apiServer {
	t.Helper()
	return newAPIServerFunc(t, func(*http.Request) (int, string) { return status, body })
}

// newAPIServerFunc answers each request from answer, for the arms that
// need discovery to succeed and the write that follows it to fail.
func newAPIServerFunc(t *testing.T, answer func(*http.Request) (int, string)) *apiServer {
	t.Helper()
	s := &apiServer{}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, body := answer(r)
		s.auth = r.Header.Get("Authorization")
		s.requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(s.Close)
	return s
}

const configMapList = `{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"configmaps","kind":"ConfigMap","namespaced":true,"verbs":["list","delete","create"]}]}`

const groupList = `{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"pods","kind":"Pod","namespaced":true,"verbs":["list","delete"]}]}`

func unauthorizedBody(msg string) string {
	return fmt.Sprintf(`{"kind":"Status","apiVersion":"v1","status":"Failure","message":%q,"reason":"Unauthorized","code":401}`, msg)
}

// TestExecBlockRunsTheCredentialPlugin is the decisive arm: a provider
// block that names host, cluster_ca_certificate and an exec block - the
// shape of every EKS root - produces a client that runs the plugin and
// sends the token it printed. Before #1114 Attrs had nowhere to put the
// exec block at all, so nothing ran and the cluster answered 401.
func TestExecBlockRunsTheCredentialPlugin(t *testing.T) {
	srv := newAPIServer(t, http.StatusOK, groupList)
	cmd, sentinel, record := tokenPlugin(t, "client.authentication.k8s.io/v1beta1", "eks-token")

	cfg, err := RestConfig(Attrs{
		Host:     srv.URL,
		Insecure: true,
		Exec: &ExecCredential{
			APIVersion: "client.authentication.k8s.io/v1beta1",
			Command:    cmd,
			Args:       []string{"eks", "get-token", "--cluster-name", "prod"},
			Env:        map[string]string{"EKS_CLUSTER": "prod", "AWS_PROFILE": "ops"},
		},
	})
	if err != nil {
		t.Fatalf("RestConfig: %v", err)
	}
	if cfg.ExecProvider == nil {
		t.Fatalf("RestConfig built no ExecProvider from the exec block: %#v", cfg)
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	serves, err := client.Serves(context.Background(), "v1", "Pod")
	if err != nil {
		t.Fatalf("Serves: %v", err)
	}
	if !serves {
		t.Fatalf("Serves(v1, Pod) = false, want true")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("the exec plugin was never run: %v", err)
	}
	if got, want := srv.auth, "Bearer eks-token"; got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("reading what the plugin saw: %v", err)
	}
	want := "eks\nget-token\n--cluster-name\nprod\nCLUSTER=prod\nPROFILE=ops\n"
	if string(got) != want {
		t.Errorf("the plugin was run as %q, want %q", got, want)
	}
}

// TestKubeconfigExecUserRunsTheCredentialPlugin pins the half of #1114
// that turned out to be already true: a kubeconfig whose user is an exec
// plugin resolves, because clientcmd sets ExecProvider from the
// auth-info and client-go's rest package links the exec credential
// provider unconditionally (rest/transport.go imports it). The issue
// records this as unwired. It is not, and this test is what would have
// noticed if it stopped being.
func TestKubeconfigExecUserRunsTheCredentialPlugin(t *testing.T) {
	srv := newAPIServer(t, http.StatusOK, groupList)
	cmd, sentinel, _ := tokenPlugin(t, "client.authentication.k8s.io/v1", "kubeconfig-token")

	dir := t.TempDir()
	kubeconfig := filepath.Join(dir, "config")
	body := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: ctx
clusters:
- name: c
  cluster:
    server: %s
    insecure-skip-tls-verify: true
contexts:
- name: ctx
  context: {cluster: c, user: u}
users:
- name: u
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: %s
      interactiveMode: Never
`, srv.URL, cmd)
	if err := os.WriteFile(kubeconfig, []byte(body), 0o600); err != nil {
		t.Fatalf("writing kubeconfig: %v", err)
	}

	cfg, err := RestConfig(Attrs{ConfigPath: kubeconfig})
	if err != nil {
		t.Fatalf("RestConfig: %v", err)
	}
	if cfg.ExecProvider == nil {
		t.Fatalf("the kubeconfig's exec user did not reach the rest config: %#v", cfg)
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := client.Serves(context.Background(), "v1", "Pod"); err != nil {
		t.Fatalf("Serves: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("the exec plugin was never run: %v", err)
	}
	if got, want := srv.auth, "Bearer kubeconfig-token"; got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
}

// TestExecBlockOverridesTheKubeconfigUser: the provider block's own
// arguments win over whatever the kubeconfig said, the precedence every
// other Attrs field already follows.
func TestExecBlockOverridesTheKubeconfigUser(t *testing.T) {
	srv := newAPIServer(t, http.StatusOK, groupList)
	blockCmd, blockRan, _ := tokenPlugin(t, "client.authentication.k8s.io/v1beta1", "from-the-block")
	fileCmd, fileRan, _ := tokenPlugin(t, "client.authentication.k8s.io/v1beta1", "from-the-kubeconfig")

	dir := t.TempDir()
	kubeconfig := filepath.Join(dir, "config")
	body := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: ctx
clusters:
- name: c
  cluster: {server: %s, insecure-skip-tls-verify: true}
contexts:
- name: ctx
  context: {cluster: c, user: u}
users:
- name: u
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: %s
`, srv.URL, fileCmd)
	if err := os.WriteFile(kubeconfig, []byte(body), 0o600); err != nil {
		t.Fatalf("writing kubeconfig: %v", err)
	}

	cfg, err := RestConfig(Attrs{
		ConfigPath: kubeconfig,
		Exec:       &ExecCredential{APIVersion: "client.authentication.k8s.io/v1beta1", Command: blockCmd},
	})
	if err != nil {
		t.Fatalf("RestConfig: %v", err)
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := client.Serves(context.Background(), "v1", "Pod"); err != nil {
		t.Fatalf("Serves: %v", err)
	}
	if _, err := os.Stat(blockRan); err != nil {
		t.Fatalf("the exec block's plugin was not run: %v", err)
	}
	if _, err := os.Stat(fileRan); err == nil {
		t.Errorf("the kubeconfig's plugin ran too; the block should have replaced it")
	}
	if got, want := srv.auth, "Bearer from-the-block"; got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
}

// TestExplicitTokenWinsOverTheExecBlock: client-go's own precedence
// (exec's UpdateTransportConfig declines when the transport already has
// token auth). Pinned so that adding the exec block cannot make a
// configuration that worked through token start shelling out.
func TestExplicitTokenWinsOverTheExecBlock(t *testing.T) {
	srv := newAPIServer(t, http.StatusOK, groupList)
	cmd, sentinel, _ := tokenPlugin(t, "client.authentication.k8s.io/v1beta1", "plugin-token")

	cfg, err := RestConfig(Attrs{
		Host:     srv.URL,
		Insecure: true,
		Token:    "explicit-token",
		Exec:     &ExecCredential{APIVersion: "client.authentication.k8s.io/v1beta1", Command: cmd},
	})
	if err != nil {
		t.Fatalf("RestConfig: %v", err)
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := client.Serves(context.Background(), "v1", "Pod"); err != nil {
		t.Fatalf("Serves: %v", err)
	}
	if got, want := srv.auth, "Bearer explicit-token"; got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Errorf("the exec plugin ran although an explicit token was set")
	}
}

// The three failure messages #1114 asks for, each driven rather than
// asserted: a real client, a real failure, and the sentence the reader
// gets in place of "cluster unreachable".

// TestNoCredentialsIsItsOwnMessage: a provider block with a host and
// nothing that can authenticate. The cluster answers 401 and the sweep
// says the configuration supplied no credential, naming what to set.
func TestNoCredentialsIsItsOwnMessage(t *testing.T) {
	srv := newAPIServer(t, http.StatusUnauthorized, unauthorizedBody("Unauthorized"))
	cfg, err := RestConfig(Attrs{Host: srv.URL, Insecure: true})
	if err != nil {
		t.Fatalf("RestConfig: %v", err)
	}
	if cr := CredentialsOf(cfg); cr.Any {
		t.Fatalf("CredentialsOf reported a credential for a bare host: %#v", cr)
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _, err = client.Kinds(context.Background(), []string{"kubernetes_pod"}, "")
	if err == nil {
		t.Fatalf("Kinds against a 401 succeeded")
	}
	mustSay(t, err, "supplies no credential", "set token, client_certificate with client_key, an exec block")
	mustNotSay(t, err, "did not answer", "exec credential plugin")
}

// TestExecPluginFailureIsItsOwnMessage: the plugin runs and exits
// non-zero. The cluster is fine and was never asked, and the message
// names the plugin rather than the cluster.
func TestExecPluginFailureIsItsOwnMessage(t *testing.T) {
	srv := newAPIServer(t, http.StatusOK, groupList)
	cmd, sentinel := execPlugin(t, "echo 'could not get token: ExpiredToken' >&2\nexit 254\n")

	cfg, err := RestConfig(Attrs{
		Host:     srv.URL,
		Insecure: true,
		Exec:     &ExecCredential{APIVersion: "client.authentication.k8s.io/v1beta1", Command: cmd},
	})
	if err != nil {
		t.Fatalf("RestConfig: %v", err)
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _, err = client.Kinds(context.Background(), []string{"kubernetes_pod"}, "")
	if err == nil {
		t.Fatalf("Kinds succeeded although the credential plugin failed")
	}
	if _, statErr := os.Stat(sentinel); statErr != nil {
		t.Fatalf("the plugin did not run at all, so this is not the failure under test: %v", statErr)
	}
	mustSay(t, err, "exec credential plugin", cmd, "did not produce a credential", "the cluster was never asked")
	mustNotSay(t, err, "did not answer", "supplies no credential")
	if srv.requests != 0 {
		t.Errorf("the server was asked %d times; a failed plugin should stop the request", srv.requests)
	}
}

// TestClusterDidNotAnswerIsItsOwnMessage: a credential that is fine and
// a host that nothing is listening on. This is the one case the old
// single "unavailable" message was ever right about, and it has to stay
// distinguishable from the other two.
func TestClusterDidNotAnswerIsItsOwnMessage(t *testing.T) {
	// A port from a server that has just been closed: nothing is
	// listening and the dial is refused rather than hanging.
	srv := newAPIServer(t, http.StatusOK, groupList)
	url := srv.URL
	srv.Close()

	cfg, err := RestConfig(Attrs{Host: url, Insecure: true, Token: "t"})
	if err != nil {
		t.Fatalf("RestConfig: %v", err)
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _, err = client.Kinds(context.Background(), []string{"kubernetes_pod"}, "")
	if err == nil {
		t.Fatalf("Kinds against a closed port succeeded")
	}
	mustSay(t, err, "the cluster did not answer")
	mustNotSay(t, err, "supplies no credential", "exec credential plugin")
}

// TestRejectedCredentialIsItsOwnMessage: the plugin ran, produced a
// token, and the cluster would not authenticate it - the EKS access
// entry that was never created. Neither the plugin nor the network is
// the problem and the message must not blame either.
func TestRejectedCredentialIsItsOwnMessage(t *testing.T) {
	srv := newAPIServer(t, http.StatusUnauthorized, unauthorizedBody("Unauthorized"))
	cmd, sentinel, _ := tokenPlugin(t, "client.authentication.k8s.io/v1beta1", "stale")

	cfg, err := RestConfig(Attrs{
		Host:     srv.URL,
		Insecure: true,
		Exec:     &ExecCredential{APIVersion: "client.authentication.k8s.io/v1beta1", Command: cmd},
	})
	if err != nil {
		t.Fatalf("RestConfig: %v", err)
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _, err = client.Kinds(context.Background(), []string{"kubernetes_pod"}, "")
	if err == nil {
		t.Fatalf("Kinds against a 401 succeeded")
	}
	if _, statErr := os.Stat(sentinel); statErr != nil {
		t.Fatalf("the plugin did not run: %v", statErr)
	}
	mustSay(t, err, "would not authenticate the credential the exec plugin", cmd, "the plugin ran")
	mustNotSay(t, err, "did not answer", "supplies no credential", "did not produce a credential")
}

// TestForbiddenIsNotCalledAConnectionFailure: a 403 is the cluster
// authenticating the caller and declining one request, and its own
// message already names the user, the verb and the resource. Classifying
// it as a connection cause would be a mask wider than its label - a
// per-kind RBAC denial in List would read as the whole cluster refusing
// the credential.
func TestForbiddenIsNotCalledAConnectionFailure(t *testing.T) {
	forbidden := &statusErr{code: http.StatusForbidden, reason: metav1.StatusReasonForbidden, message: `pods is forbidden: User "u" cannot list resource "pods"`}
	cr := Credentials{Known: true, Any: true, Exec: "aws"}
	cause, sentence := cr.Diagnose(forbidden)
	if cause != CauseUnclassified || sentence != "" {
		t.Fatalf("Diagnose(403) = %q, %q; want it passed through unclassified", cause, sentence)
	}
	if got := cr.explain(forbidden); got.Error() != forbidden.Error() {
		t.Errorf("explain rewrote a 403: %v", got)
	}
}

// TestExecFailurePhrasesAreClientGos drives four real exec-plugin
// failures through client-go and checks each is recognised. The
// classifier matches on client-go's wording because that package
// returns plain errors with no type to match on, so this is what keeps
// a client-go upgrade that rewords one from silently costing a
// classification.
func TestExecFailurePhrasesAreClientGos(t *testing.T) {
	cases := []struct {
		name       string
		apiVersion string
		body       string
		// atBuild is a failure client-go raises when the clients are
		// built rather than when a request is made.
		atBuild bool
	}{
		{name: "exits non-zero", apiVersion: "client.authentication.k8s.io/v1beta1", body: "exit 1\n"},
		{name: "prints nothing usable", apiVersion: "client.authentication.k8s.io/v1beta1", body: "echo not-json\n"},
		{
			name:       "returns no token",
			apiVersion: "client.authentication.k8s.io/v1beta1",
			body:       "echo '{\"apiVersion\":\"client.authentication.k8s.io/v1beta1\",\"kind\":\"ExecCredential\",\"status\":{}}'\n",
		},
		{name: "names an api_version client-go does not know", apiVersion: "client.authentication.k8s.io/v0", body: "exit 0\n", atBuild: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newAPIServer(t, http.StatusOK, groupList)
			cmd, _ := execPlugin(t, tc.body)
			cfg, err := RestConfig(Attrs{
				Host:     srv.URL,
				Insecure: true,
				Exec:     &ExecCredential{APIVersion: tc.apiVersion, Command: cmd},
			})
			if err != nil {
				t.Fatalf("RestConfig: %v", err)
			}
			client, err := New(cfg)
			if tc.atBuild {
				if err == nil {
					t.Fatalf("New succeeded for %s", tc.name)
				}
				mustSay(t, err, "exec credential plugin")
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, _, err = client.Kinds(context.Background(), []string{"kubernetes_pod"}, "")
			if err == nil {
				t.Fatalf("Kinds succeeded for %s", tc.name)
			}
			if !isExecFailure(err) {
				t.Fatalf("client-go's wording for %s is not recognised as an exec failure: %v", tc.name, err)
			}
			mustSay(t, err, "did not produce a credential")
		})
	}
}

// TestMissingPluginIsAnExecFailure: the command is not there at all,
// which is the shape of an EKS root run where the AWS CLI is not
// installed.
func TestMissingPluginIsAnExecFailure(t *testing.T) {
	srv := newAPIServer(t, http.StatusOK, groupList)
	cfg, err := RestConfig(Attrs{
		Host:     srv.URL,
		Insecure: true,
		Exec:     &ExecCredential{APIVersion: "client.authentication.k8s.io/v1beta1", Command: filepath.Join(t.TempDir(), "no-such-aws")},
	})
	if err != nil {
		t.Fatalf("RestConfig: %v", err)
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, _, err = client.Kinds(context.Background(), []string{"kubernetes_pod"}, ""); err == nil {
		t.Fatalf("Kinds succeeded with no plugin on disk")
	}
	mustSay(t, err, "exec credential plugin", "did not produce a credential")
}

// TestCredentialsOfReadsEveryIdentifyingField: the no-credentials arm
// fires on Any being false, so a field that identifies a user and is not
// counted here would report "no credential" over a configuration that
// has one.
func TestCredentialsOfReadsEveryIdentifyingField(t *testing.T) {
	for _, tc := range []struct {
		name  string
		attrs Attrs
		want  bool
	}{
		{name: "bare host", attrs: Attrs{Host: "https://h"}, want: false},
		{name: "token", attrs: Attrs{Host: "https://h", Token: "t"}, want: true},
		{name: "client certificate", attrs: Attrs{Host: "https://h", ClientCertificate: "c", ClientKey: "k"}, want: true},
		{name: "exec", attrs: Attrs{Host: "https://h", Exec: &ExecCredential{Command: "aws"}}, want: true},
		{name: "ca only", attrs: Attrs{Host: "https://h", ClusterCACertificate: "ca"}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := RestConfig(tc.attrs)
			if err != nil {
				t.Fatalf("RestConfig: %v", err)
			}
			if got := CredentialsOf(cfg).Any; got != tc.want {
				t.Errorf("CredentialsOf(%s).Any = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestNewWithClaimsNoCredentials: a client built over already-made
// clients knows nothing about how it authenticates, so it must not tell
// a reader there were no credentials. Without Known this test's 401
// would come back as the no-credentials message over a fake.
func TestNewWithClaimsNoCredentials(t *testing.T) {
	cr := Credentials{}
	cause, _ := cr.Diagnose(&statusErr{code: http.StatusUnauthorized, reason: metav1.StatusReasonUnauthorized, message: "Unauthorized"})
	if cause != CauseRejected {
		t.Fatalf("Diagnose over an unknown-credential client = %q, want %q", cause, CauseRejected)
	}
}

// statusErr is an error carrying an API status the way a real one from
// the server does, for the two arms that need a status without a server.
type statusErr struct {
	code    int32
	reason  metav1.StatusReason
	message string
}

func (e *statusErr) Error() string { return e.message }
func (e *statusErr) Status() metav1.Status {
	return metav1.Status{Status: metav1.StatusFailure, Code: e.code, Reason: e.reason, Message: e.message}
}

func mustSay(t *testing.T, err error, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("the message does not say %q:\n  %v", w, err)
		}
	}
}

func mustNotSay(t *testing.T, err error, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(err.Error(), w) {
			t.Errorf("the message says %q, which is a different failure:\n  %v", w, err)
		}
	}
}

// TestDryRunDoesNotReportA401AsTheServerRefusingTheManifest: the dry run
// reads a 4xx as the API server's verdict on the object, which is right
// for a 422 or a 403 and wrong for a 401. A credential the cluster will
// not authenticate refuses every object equally, and calling that "the
// server rejected your manifest" sends the reader to the manifest.
func TestDryRunDoesNotReportA401AsTheServerRefusingTheManifest(t *testing.T) {
	// Discovery answers and the write does not: the credential expired
	// between the two calls, which is how a cached exec credential
	// actually fails mid-run. Answering 401 to everything would fail at
	// discovery instead and never reach the line under test.
	srv := newAPIServerFunc(t, func(r *http.Request) (int, string) {
		if r.Method == http.MethodGet {
			return http.StatusOK, configMapList
		}
		return http.StatusUnauthorized, unauthorizedBody("Unauthorized")
	})
	cmd, _, _ := tokenPlugin(t, "client.authentication.k8s.io/v1beta1", "stale")
	cfg, err := RestConfig(Attrs{
		Host:     srv.URL,
		Insecure: true,
		Exec:     &ExecCredential{APIVersion: "client.authentication.k8s.io/v1beta1", Command: cmd},
	})
	if err != nil {
		t.Fatalf("RestConfig: %v", err)
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	manifest := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "c", "namespace": "default"},
	}
	res, err := client.DryRun(context.Background(), manifest, false)
	if err == nil {
		t.Fatalf("DryRun returned %+v and no error; a 401 is not an answer about the manifest", res)
	}
	if res.Message != "" {
		t.Errorf("DryRun reported the 401 as the server's rejection of the manifest: %q", res.Message)
	}
	mustSay(t, err, "would not authenticate the credential the exec plugin")
}
