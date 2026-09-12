// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"context"
	"fmt"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	restclient "k8s.io/client-go/rest"
)

// Kind is one listable kind the cluster serves that the provider has a
// resource type for.
type Kind struct {
	GVR        schema.GroupVersionResource
	Kind       string
	Namespaced bool
	// TypeNames are the provider types that manage this kind, sorted.
	TypeNames []string
}

// Object is one live object carrying the estate's label whose controller,
// if any, is not the one that made it - metadata.ownerReferences is empty.
type Object struct {
	Kind      string
	Namespace string
	Name      string
	Labels    map[string]string
	// ImportID is the provider's documented import id: NAMESPACE/NAME, or
	// NAME for a cluster-scoped kind.
	ImportID string
}

// Sweeper is what discovery asks of a Kubernetes sweep, so a test can
// stand in for a cluster.
type Sweeper interface {
	// Kinds are the listable kinds among typeNames' kinds, in a stable
	// order, and the provider types whose kinds the cluster does not
	// serve at all.
	Kinds(ctx context.Context, typeNames []string) (kinds []Kind, unserved []string, err error)
	// List returns every object of k carrying label key=value, excluding
	// controller-owned ones, and how many of those it excluded.
	List(ctx context.Context, k Kind, key, value string) (objects []Object, ownerSkipped int, err error)
}

// Client is [Sweeper] over a real API server.
type Client struct {
	disc discovery.DiscoveryInterface
	dyn  dynamic.Interface
}

// New connects. Nothing is called until [Client.Kinds] or [Client.List].
func New(cfg *restclient.Config) (*Client, error) {
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("discovery client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("dynamic client: %w", err)
	}
	return &Client{disc: disc, dyn: dyn}, nil
}

// NewWith is [New] over already-built clients, for tests.
func NewWith(disc discovery.DiscoveryInterface, dyn dynamic.Interface) *Client {
	return &Client{disc: disc, dyn: dyn}
}

// Kinds implements [Sweeper]: every group's preferred version of every
// resource, joined to typeNames by kind. A kind served by more than one
// group (Event in the core group and in events.k8s.io) is listed once per
// group, since those are distinct resources; a kind served at more than
// one version within a group is listed at the group's preferred version
// only, since those are one resource.
func (c *Client) Kinds(ctx context.Context, typeNames []string) ([]Kind, []string, error) {
	byKind := KindTypes(typeNames)
	groups, lists, err := c.disc.ServerGroupsAndResources()
	if err != nil && len(lists) == 0 {
		// A partial discovery failure (one aggregated API group down)
		// still returns the groups that answered; only a total failure
		// is fatal here.
		return nil, nil, fmt.Errorf("API discovery: %w", err)
	}
	preferred := map[string]string{} // group -> its preferred GroupVersion
	for _, g := range groups {
		if g != nil {
			preferred[g.Name] = g.PreferredVersion.GroupVersion
		}
	}
	served := map[string]bool{}
	var kinds []Kind
	for _, list := range lists {
		if list == nil {
			continue
		}
		gv, gvErr := schema.ParseGroupVersion(list.GroupVersion)
		if gvErr != nil {
			continue
		}
		if want, ok := preferred[gv.Group]; ok && want != list.GroupVersion {
			continue
		}
		for _, r := range list.APIResources {
			if strings.Contains(r.Name, "/") {
				continue // a subresource: pods/status, deployments/scale
			}
			types, ok := byKind[r.Kind]
			if !ok || !hasVerb(r.Verbs, "list") || !hasVerb(r.Verbs, "delete") {
				continue
			}
			served[r.Kind] = true
			kinds = append(kinds, Kind{
				GVR:        gv.WithResource(r.Name),
				Kind:       r.Kind,
				Namespaced: r.Namespaced,
				TypeNames:  types,
			})
		}
	}
	sort.Slice(kinds, func(i, j int) bool {
		if kinds[i].Kind != kinds[j].Kind {
			return kinds[i].Kind < kinds[j].Kind
		}
		return kinds[i].GVR.String() < kinds[j].GVR.String()
	})
	var unserved []string
	for kind, types := range byKind {
		if !served[kind] {
			unserved = append(unserved, types...)
		}
	}
	sort.Strings(unserved)
	return kinds, unserved, nil
}

// List implements [Sweeper]: one cluster-wide, label-selected list.
func (c *Client) List(ctx context.Context, k Kind, key, value string) ([]Object, int, error) {
	opts := metav1.ListOptions{LabelSelector: key + "=" + value}
	res := c.dyn.Resource(k.GVR)
	var (
		items   []Object
		skipped int
		cont    string
	)
	for {
		opts.Continue = cont
		ul, err := res.Namespace(metav1.NamespaceAll).List(ctx, opts)
		if err != nil {
			return nil, 0, err
		}
		for _, item := range ul.Items {
			if ControllerMade(&item) {
				skipped++
				continue
			}
			o := Object{
				Kind:      k.Kind,
				Namespace: item.GetNamespace(),
				Name:      item.GetName(),
				Labels:    item.GetLabels(),
				ImportID:  item.GetName(),
			}
			if k.Namespaced {
				o.ImportID = item.GetNamespace() + "/" + item.GetName()
			}
			items = append(items, o)
		}
		cont = ul.GetContinue()
		if cont == "" {
			break
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ImportID < items[j].ImportID })
	return items, skipped, nil
}

func hasVerb(verbs []string, verb string) bool {
	for _, v := range verbs {
		if v == verb {
			return true
		}
	}
	return false
}

// controlPlaneManagers are the field managers a controller-made object is
// written by and nothing else ever is: a user's object always carries at
// least one other manager (the provider's own, kubectl's, a CI tool's).
var controlPlaneManagers = map[string]bool{
	"kube-controller-manager": true,
	"kube-scheduler":          true,
	"kubelet":                 true,
	"kube-apiserver":          true,
}

// ControllerMade reports whether a live object was made by a controller
// rather than declared by anyone, on two signals, either sufficient:
//
//   - a non-empty metadata.ownerReferences, which every object a
//     controller creates from a template carries (a ReplicaSet's from its
//     Deployment, a Pod's from its ReplicaSet, an EndpointSlice's from its
//     Service, a PVC's from its StatefulSet);
//   - metadata.managedFields naming only control-plane managers, which
//     catches the controller-made objects that carry no owner reference -
//     the legacy core/v1 Endpoints the endpoints controller mirrors a
//     Service's labels onto, found by #1065's own scenario the first time
//     it ran, is the confirmed instance. An object nobody but the control
//     plane has ever written was written by nobody who declares things.
//
// An object with no managedFields at all (a cluster older than the field
// manager mechanism, or a client that strips them) is judged on owner
// references alone.
func ControllerMade(obj *unstructured.Unstructured) bool {
	if len(obj.GetOwnerReferences()) > 0 {
		return true
	}
	managed := obj.GetManagedFields()
	if len(managed) == 0 {
		return false
	}
	for _, entry := range managed {
		if !controlPlaneManagers[entry.Manager] {
			return false
		}
	}
	return true
}
