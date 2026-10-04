// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package managedk8s

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	container "google.golang.org/api/container/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// gkeGetPermission is the IAM permission projects.locations.clusters.get
// needs.
const gkeGetPermission = "container.clusters.get"

// gke asks projects.locations.clusters.get with Application Default
// Credentials, the chain the gcs record store and the google provider use.
func (r *Reader) gke(ctx context.Context, cp staterecord.ManagedControlPlane) (staterecord.ControlPlaneEncryption, error) {
	var out staterecord.ControlPlaneEncryption
	var opts []option.ClientOption
	if r.GKEEndpoint != "" {
		opts = append(opts, option.WithEndpoint(r.GKEEndpoint))
	}
	if r.gkeNoAuth {
		opts = append(opts, option.WithoutAuthentication())
		if r.HTTPClient != nil {
			opts = append(opts, option.WithHTTPClient(r.HTTPClient))
		}
	}
	svc, err := container.NewService(ctx, opts...)
	if err != nil {
		return out, fmt.Errorf("building the GKE client: %w", err)
	}
	name := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", cp.Project, cp.Location, cp.Name)
	c, err := svc.Projects.Locations.Clusters.Get(name).Context(ctx).Do()
	if err != nil {
		var gerr *googleapi.Error
		if errors.As(err, &gerr) {
			switch gerr.Code {
			case http.StatusForbidden:
				return out, &staterecord.ControlPlaneDeniedError{Action: gkeGetPermission, Err: err}
			case http.StatusNotFound:
				return out, &staterecord.ControlPlaneNotFoundError{Err: err}
			}
		}
		return out, fmt.Errorf("clusters.get %s: %w", name, err)
	}
	return gkeEncryption(c), nil
}

// gkeEndpoints is every address GKE reports for the cluster's API server:
// the classic endpoint, the private cluster's two, and the control plane
// endpoints config's DNS and IP ones.
func gkeEndpoints(c *container.Cluster) []string {
	eps := appendNonEmpty(nil, c.Endpoint)
	if p := c.PrivateClusterConfig; p != nil {
		eps = appendNonEmpty(eps, p.PrivateEndpoint, p.PublicEndpoint)
	}
	if ce := c.ControlPlaneEndpointsConfig; ce != nil {
		if d := ce.DnsEndpointConfig; d != nil {
			eps = appendNonEmpty(eps, d.Endpoint)
		}
		if ip := ce.IpEndpointsConfig; ip != nil {
			eps = appendNonEmpty(eps, ip.PublicEndpoint, ip.PrivateEndpoint)
		}
	}
	return eps
}

// gkeEncryption is the verdict a cluster's databaseEncryption carries.
//
// currentState is what is in force and is read first; state is only what was
// asked for, and is read when currentState is absent. A transition in either
// direction, and an error part-way through one, settle nothing, so they are
// undetermined rather than guessed.
func gkeEncryption(c *container.Cluster) staterecord.ControlPlaneEncryption {
	out := staterecord.ControlPlaneEncryption{Cluster: c.SelfLink, Endpoints: gkeEndpoints(c)}
	if out.Cluster == "" {
		out.Cluster = c.Name
	}
	de := c.DatabaseEncryption
	if de == nil {
		out.Verdict = staterecord.EncryptionOff
		out.Detail = "the cluster has no databaseEncryption, so application-layer secrets encryption is off; turn it on with `gcloud container clusters update --database-encryption-key=<Cloud KMS key>`"
		return out
	}
	key := de.KeyName
	if key == "" {
		key = "(no key named)"
	}
	switch de.CurrentState {
	case "CURRENT_STATE_ENCRYPTED", "CURRENT_STATE_ALL_OBJECTS_ENCRYPTION_ENABLED":
		out.Verdict = staterecord.EncryptionOn
		out.Detail = fmt.Sprintf("databaseEncryption.currentState is %s with Cloud KMS key %s", de.CurrentState, key)
	case "CURRENT_STATE_DECRYPTED", "CURRENT_STATE_DECRYPTION_PENDING":
		out.Verdict = staterecord.EncryptionOff
		out.Detail = fmt.Sprintf("databaseEncryption.currentState is %s, so application-layer secrets encryption is off or being turned off", de.CurrentState)
	case "", "CURRENT_STATE_UNSPECIFIED":
		switch de.State {
		case "ENCRYPTED", "ALL_OBJECTS_ENCRYPTION_ENABLED":
			out.Verdict = staterecord.EncryptionOn
			out.Detail = fmt.Sprintf("databaseEncryption.state is %s with Cloud KMS key %s (the API reported no currentState)", de.State, key)
		case "DECRYPTED":
			out.Verdict = staterecord.EncryptionOff
			out.Detail = "databaseEncryption.state is DECRYPTED, so application-layer secrets encryption is off; turn it on with `gcloud container clusters update --database-encryption-key=<Cloud KMS key>`"
		default:
			out.Detail = fmt.Sprintf("databaseEncryption reports state %q and no currentState", de.State)
		}
	default:
		out.Detail = fmt.Sprintf("databaseEncryption.currentState is %s (state %s, key %s), a transition or an error, so whether every Secret is encrypted is not settled; run again once it finishes", de.CurrentState, de.State, key)
	}
	return out
}
