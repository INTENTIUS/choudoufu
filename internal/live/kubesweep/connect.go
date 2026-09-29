// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	restclient "k8s.io/client-go/rest"
)

// Four capabilities read the cluster - the orphan sweep, the
// missing-CRD check, the manifest dry run and live-ls - and before
// GitHub issue #1114 every way of failing to read it arrived at the
// reader as the same "Kubernetes sweep unavailable", with the raw
// client-go error behind it. On EKS that is the common case rather than
// the exotic one, and "unavailable" is indistinguishable from a network
// problem: a user cannot tell a credential choudoufu never assembled
// from a cluster that is genuinely down.
//
// So a cluster call's failure is classified once, here, and the cause is
// put in front of the error before it leaves the client. Every existing
// warning prints the error with %s and so inherits the sentence with no
// edit of its own.

// ConnectCause names why a cluster call produced no answer.
type ConnectCause string

const (
	// CauseUnclassified is a failure this does not recognise: it is
	// passed through unchanged rather than given a cause it might not
	// have. A per-kind RBAC denial on one list is one of these - it is
	// not a connection problem at all.
	CauseUnclassified ConnectCause = ""
	// CauseNoCredentials is the cluster refusing a request that carried
	// no credential, because the configuration supplied none: no token,
	// no client certificate, no exec block and no kubeconfig the loader
	// could read.
	CauseNoCredentials ConnectCause = "no credentials"
	// CauseExecPlugin is the credential plugin failing: it is not on
	// PATH, it exited non-zero, it printed something that is not an
	// ExecCredential, or it answered in a protocol version the exec
	// block does not name. The cluster was never asked.
	CauseExecPlugin ConnectCause = "exec plugin"
	// CauseRejected is the cluster answering 401 with a credential in
	// hand: the connection is good and the credential is not accepted.
	// On EKS an expired or wrong-cluster token from the plugin lands
	// here, and the plugin itself is fine.
	CauseRejected ConnectCause = "credential rejected"
	// CauseUnreachable is the cluster not answering at all: the dial
	// failed, the TLS handshake failed, the deadline passed. This is the
	// one thing "unavailable" used to mean and the only one it was ever
	// right about.
	CauseUnreachable ConnectCause = "unreachable"
)

// Credentials is what a built client knows about how it was told to
// authenticate, kept so that a refusal from the cluster can be reported
// against what was actually sent.
type Credentials struct {
	// Known distinguishes a client built from a [restclient.Config] from
	// one built over already-made clients ([NewWith], which tests use):
	// the second knows nothing about credentials, so it must not claim
	// there were none.
	Known bool
	// Exec is the credential plugin's command when one is configured,
	// whether it came from the provider block's exec block or from a
	// kubeconfig user, and empty when none is.
	Exec string
	// Any reports that something in the configuration can identify a
	// user. False means every request goes out anonymous.
	Any bool
}

// CredentialsOf reads how cfg authenticates. The set of fields is
// client-go's own canIdentifyUser (tools/clientcmd/client_config.go)
// widened by the two file-backed forms a rest.Config can carry that a
// kubeconfig's merged config cannot.
func CredentialsOf(cfg *restclient.Config) Credentials {
	cr := Credentials{Known: true}
	if cfg == nil {
		return cr
	}
	if cfg.ExecProvider != nil {
		cr.Exec = cfg.ExecProvider.Command
	}
	switch {
	case cfg.ExecProvider != nil,
		cfg.AuthProvider != nil,
		cfg.BearerToken != "",
		cfg.BearerTokenFile != "",
		cfg.Username != "",
		len(cfg.TLSClientConfig.CertData) > 0,
		cfg.TLSClientConfig.CertFile != "",
		cfg.TLSClientConfig.KeyFile != "":
		cr.Any = true
	}
	return cr
}

// Diagnose classifies a cluster call's failure and returns the sentence
// that names the cause, or CauseUnclassified and an empty string for one
// it does not recognise.
func (cr Credentials) Diagnose(err error) (ConnectCause, string) {
	if err == nil {
		return CauseUnclassified, ""
	}
	// The exec plugin is asked before the request is sent, so its
	// failure is never a status from the server. Test it first: a
	// plugin that failed means the cluster was never reached, whatever
	// the transport wrapped the error in.
	if isExecFailure(err) {
		// The plugin's own words are not in err: client-go gives the
		// plugin this process's standard error directly
		// (plugin/pkg/client/auth/exec's Authenticator.stderr), so
		// "could not get token: ExpiredToken" is already on the
		// reader's terminal above the warning. What is missing, and
		// what this supplies, is that the line came from a credential
		// plugin and that the cluster was never asked.
		who := "the exec credential plugin"
		if cr.Exec != "" {
			who = fmt.Sprintf("the exec credential plugin %q", cr.Exec)
		}
		return CauseExecPlugin, who + " did not produce a credential, so the cluster was never asked"
	}
	// Only 401 is classified. A 403 is the cluster authenticating the
	// caller and declining one request, and the server's own message
	// already names the user, the verb and the resource - far better
	// than any prefix this could put in front of it. Calling that a
	// connection cause would also be a mask wider than its label: a
	// per-kind RBAC denial in [Client.List] would read as the whole
	// cluster refusing the credential.
	if apierrors.IsUnauthorized(err) {
		if cr.Known && !cr.Any {
			return CauseNoCredentials, "the cluster refused an anonymous request and this provider configuration supplies no credential: set token, client_certificate with client_key, an exec block, or a kubeconfig through config_path, config_paths or KUBECONFIG"
		}
		if cr.Exec != "" {
			return CauseRejected, fmt.Sprintf("the cluster answered and would not authenticate the credential the exec plugin %q produced, so the plugin ran and the cluster does not accept what it returned", cr.Exec)
		}
		return CauseRejected, "the cluster answered and would not authenticate the credential this provider configuration supplies, so the connection is good and the credential is not"
	}
	if unreachable(err) {
		return CauseUnreachable, "the cluster did not answer"
	}
	return CauseUnclassified, ""
}

// explain puts the cause in front of a failed cluster call, so that the
// warning that prints it says which of the four things went wrong.
func (cr Credentials) explain(err error) error {
	cause, sentence := cr.Diagnose(err)
	if cause == CauseUnclassified {
		return err
	}
	return fmt.Errorf("%s: %w", sentence, err)
}

// execPhrases are the exec credential provider's own wording. client-go
// returns plain errors from
// k8s.io/client-go/plugin/pkg/client/auth/exec with no type to match
// on, so the text is what there is. Each of these is emitted at exactly
// one place in that package:
//
//   - "getting credentials: " wraps every failure of a plugin that ran
//     or could not be started (exec.go, Authenticator.getCreds).
//   - "exec plugin: invalid apiVersion" and the "exec plugin ..."
//     protocol complaints are the plugin's answer being unusable.
//   - "unknown interactiveMode" is an ExecConfig this fork built wrong,
//     which is a bug here rather than in the user's configuration, and
//     is still an exec-plugin failure from the reader's side.
//
// A phrase moving in a client-go upgrade costs a classification, not a
// correctness: the failure falls back to CauseUnclassified and prints
// the way it did before #1114. TestExecFailurePhrasesAreClientGos pins
// them against the module in go.mod so the loss is noticed.
var execPhrases = []string{
	"getting credentials: ",
	"exec plugin: ",
	"exec plugin didn't return",
	"exec plugin returned only",
	"unknown interactiveMode",
}

func isExecFailure(err error) bool {
	msg := err.Error()
	for _, p := range execPhrases {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// unreachable reports a failure that never became an answer from the API
// server. Anything the server itself said arrives as an APIStatus - a
// 404 from discovery, a 403 from RBAC, a 500 from an aggregated API - so
// the absence of one, for an error that got as far as a cluster call, is
// the cluster not answering: a refused dial, a TLS handshake that
// failed, a deadline, a DNS name that does not resolve.
func unreachable(err error) bool {
	var status apierrors.APIStatus
	return !errors.As(err, &status)
}
