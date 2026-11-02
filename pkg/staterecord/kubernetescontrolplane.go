// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// Encryption at rest on a managed control plane (GitHub issue #1524).
//
// On EKS, GKE and AKS the API server is not a Pod in the user's cluster, so
// [checkEncryptionAtRest]'s reading of --encryption-provider-config off the
// API server's static Pod has nothing to read, and the finding used to be
// NOT CHECKED on every managed cluster. The setting is not gone, it moved:
// each provider keeps it on its own description of the cluster, which its own
// API answers.
//
//   - EKS: DescribeCluster's encryptionConfig, the KMS key envelope
//     encryption of secrets uses. A cluster at 1.28 or later with no
//     customer key is still envelope-encrypted, with an AWS owned key, which
//     is EKS's documented default since 2025 and which DescribeCluster does
//     not report as an entry.
//   - GKE: the cluster's databaseEncryption, application-layer secrets
//     encryption with a Cloud KMS key. Its currentState is what is in force;
//     its state is only what was asked for.
//   - AKS: the managed cluster's securityProfile.azureKeyVaultKms, KMS etcd
//     encryption with an Azure Key Vault key.
//
// What is asserted is what the kind check asserts: that the API server
// encrypts a Secret before it is written to etcd. Every one of these
// providers also encrypts the disks etcd sits on, and that is not this
// assertion: a reader of etcd itself, or of a backup of it, reads a record
// stored under disk encryption in the clear. GKE's own API spells the
// distinction out ("stored in plain text (at etcd level) - this is unrelated
// to Compute Engine level full disk encryption").
//
// # The answer is about a cluster, so the cluster is checked first
//
// A provider API describes whatever cluster it is NAMED, and the name comes
// from configuration or from an exec plugin's arguments, neither of which is
// the connection the records actually travel over. So the description is
// trusted only when one of the endpoints the provider reports for it is the
// host this store's connection reaches. Otherwise it is NOT CHECKED, saying
// both, because a green read off the wrong cluster is the one outcome worse
// than no answer.

// ManagedControlPlane names a cluster to its provider's API: which provider,
// and the coordinates that provider's API takes. Only the fields the
// provider uses are read.
type ManagedControlPlane struct {
	// Provider is "eks", "gke" or "aks".
	Provider string

	// Name is the cluster's name in its provider. Required for all three.
	Name string

	// Region is EKS's AWS region.
	Region string

	// Project and Location are GKE's: the Google Cloud project and the
	// cluster's location (a region or a zone).
	Project  string
	Location string

	// ResourceGroup and SubscriptionID are AKS's.
	ResourceGroup  string
	SubscriptionID string

	// Source says where this identification came from, for the finding's
	// text: the record_store block's control_plane block, or what was
	// inferred from the exec credential plugin's arguments.
	Source string
}

// Managed control plane providers, as the control_plane block's label spells
// them.
const (
	ControlPlaneEKS = "eks"
	ControlPlaneGKE = "gke"
	ControlPlaneAKS = "aks"
)

// ControlPlaneProviders is every provider a control_plane block may name.
var ControlPlaneProviders = []string{ControlPlaneEKS, ControlPlaneGKE, ControlPlaneAKS}

// Describe is the cluster named the way an operator would find it in that
// provider's console.
func (cp ManagedControlPlane) Describe() string {
	switch cp.Provider {
	case ControlPlaneEKS:
		return fmt.Sprintf("EKS cluster %q in %s", cp.Name, orUnset(cp.Region))
	case ControlPlaneGKE:
		return fmt.Sprintf("GKE cluster %q in project %s, location %s", cp.Name, orUnset(cp.Project), orUnset(cp.Location))
	case ControlPlaneAKS:
		return fmt.Sprintf("AKS cluster %q in resource group %s, subscription %s", cp.Name, orUnset(cp.ResourceGroup), orUnset(cp.SubscriptionID))
	}
	return fmt.Sprintf("%s cluster %q", cp.Provider, cp.Name)
}

func orUnset(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

// EncryptionVerdict is what a provider's description of a cluster says about
// Secrets in etcd.
type EncryptionVerdict uint8

const (
	// EncryptionUndetermined is the zero value: a description that did not
	// settle it, such as a GKE cluster part-way through turning encryption
	// on. It is NOT CHECKED, never a pass.
	EncryptionUndetermined EncryptionVerdict = iota
	// EncryptionOn: the API server encrypts Secrets before etcd.
	EncryptionOn
	// EncryptionOff: it does not.
	EncryptionOff
)

// ControlPlaneEncryption is one provider's answer about one cluster.
type ControlPlaneEncryption struct {
	// Cluster is the provider's own name for what it described: an ARN, a
	// selfLink, a resource ID.
	Cluster string

	// Endpoints is every address the provider says this cluster's API server
	// answers at. One of them has to be the host the store's connection
	// reaches before anything else here is believed.
	Endpoints []string

	// Verdict is the answer.
	Verdict EncryptionVerdict

	// Detail is the provider's own fields, in a sentence, saying why the
	// verdict is what it is: the key, the state, the version that made it a
	// default. Always set.
	Detail string
}

// ControlPlaneReader asks a managed control plane's provider about a cluster.
// internal/live/managedk8s holds the three real ones; tests hold fakes.
type ControlPlaneReader interface {
	SecretsEncryption(ctx context.Context, cp ManagedControlPlane) (ControlPlaneEncryption, error)
}

// ControlPlaneDeniedError is a provider refusing this identity the read. It
// is NOT CHECKED, the same as a forbidden List of kube-system is: the setting
// exists, this identity may not see it.
type ControlPlaneDeniedError struct {
	// Action is the provider permission the read needs, spelled the way
	// that provider's IAM spells it.
	Action string
	Err    error
}

func (e *ControlPlaneDeniedError) Error() string {
	return fmt.Sprintf("this identity may not call %s: %v", e.Action, e.Err)
}

func (e *ControlPlaneDeniedError) Unwrap() error { return e.Err }

// ControlPlaneNotFoundError is a provider answering that it has no cluster by
// that name. A control_plane block that names a cluster that is not there is
// a configuration error the finding says by name.
type ControlPlaneNotFoundError struct {
	Err error
}

func (e *ControlPlaneNotFoundError) Error() string {
	return fmt.Sprintf("the provider has no such cluster: %v", e.Err)
}

func (e *ControlPlaneNotFoundError) Unwrap() error { return e.Err }

// controlPlaneReadTimeout bounds the one provider call a first contact makes.
const controlPlaneReadTimeout = 30 * time.Second

// checkManagedEncryption is assertion 3 on a cluster whose control plane is
// named: the provider's own description of the setting, believed only for
// the cluster this connection reaches.
func checkManagedEncryption(ctx context.Context, opts ClusterContractOptions) Finding {
	f := Finding{Setting: ClusterEncryptionAtRest}
	cp := *opts.ControlPlane
	where := cp.Describe()
	if cp.Source != "" {
		where += " (" + cp.Source + ")"
	}

	if opts.ControlPlaneReader == nil {
		f.Outcome = NotChecked
		f.Found = fmt.Sprintf("not checked: %s names a managed control plane and this build was given no way to ask its provider", where)
		return f
	}

	// Bounded, because a credential chain with nothing to find can spend a
	// long time looking (IMDS, a metadata server), and a first contact that
	// hangs on it is worse than one that says it could not ask.
	askCtx, cancel := context.WithTimeout(ctx, controlPlaneReadTimeout)
	defer cancel()
	got, err := opts.ControlPlaneReader.SecretsEncryption(askCtx, cp)
	if err != nil {
		f.Outcome = NotChecked
		var denied *ControlPlaneDeniedError
		var missing *ControlPlaneNotFoundError
		switch {
		case errors.As(err, &denied):
			f.Found = fmt.Sprintf("not readable from here, not checked: whether Secrets are encrypted at rest on %s is that provider's setting, and %s", where, denied.Error())
		case errors.As(err, &missing):
			f.Found = fmt.Sprintf("not checked: %s was asked for and %s; correct the record_store block's control_plane block", where, missing.Error())
		default:
			f.Found = fmt.Sprintf("not checked: asking the provider about %s failed (%s)", where, err)
		}
		return f
	}

	if !endpointMatches(opts.APIServerHost, got.Endpoints) {
		f.Outcome = NotChecked
		f.Found = fmt.Sprintf("not checked: the provider describes %s as answering at %s, and this store's connection reaches %s, so what it says about encryption is about a different cluster, or this connection goes through something in between; either way it is not an answer about where the records are",
			where, joinOrNone(got.Endpoints), orUnset(opts.APIServerHost))
		return f
	}

	switch got.Verdict {
	case EncryptionOn:
		f.Outcome = Passed
		f.Found = fmt.Sprintf("%s, read from the provider: %s", where, got.Detail)
	case EncryptionOff:
		f.Outcome = Failed
		f.Found = fmt.Sprintf("%s, read from the provider: %s; the API server writes Secret data to etcd without encrypting it, so anything that reads etcd or a backup of it reads every record", where, got.Detail)
	default:
		f.Outcome = NotChecked
		f.Found = fmt.Sprintf("not checked: %s, read from the provider, did not settle it: %s", where, got.Detail)
	}
	return f
}

// endpointMatches reports whether host, the store connection's API server
// address, is one of endpoints. Both sides are reduced to a lower-case host
// with the default HTTPS port dropped, since a provider reports a bare IP or
// an https URL and a kubeconfig names either.
func endpointMatches(host string, endpoints []string) bool {
	want := normalizeEndpoint(host)
	if want == "" {
		return false
	}
	for _, e := range endpoints {
		if normalizeEndpoint(e) == want {
			return true
		}
	}
	return false
}

func normalizeEndpoint(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	h := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	port := u.Port()
	if port == "" || port == "443" {
		return h
	}
	return net.JoinHostPort(h, port)
}
