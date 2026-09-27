// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// Kubernetes is the hashicorp/kubernetes family: tofu-estate alone, in
// metadata[0].labels or, for kubernetes_manifest, in
// manifest.metadata.labels.
var Kubernetes Substrate = kubernetes{}

type kubernetes struct{}

func (kubernetes) Name() string { return "kubernetes" }

func (kubernetes) Surfaces() []markers.Surface {
	return []markers.Surface{markers.SurfaceLabels, markers.SurfaceManifest}
}

func (kubernetes) SurfaceOf(block *configschema.Block) (markers.Surface, bool) {
	if markers.ManifestSurface(block) {
		return markers.SurfaceManifest, true
	}
	if _, ok := markers.LabelSurface(block); ok {
		return markers.SurfaceLabels, true
	}
	return "", false
}

// MarkersOf on the manifest surface reads the prior manifest, which is
// where the projection's mirror of the live object's own estate label
// lands (#1079).
func (kubernetes) MarkersOf(surface markers.Surface, obj cty.Value) (map[string]string, bool) {
	switch surface {
	case markers.SurfaceLabels:
		return markers.LabelsOf(obj)
	case markers.SurfaceManifest:
		return markers.ManifestLabelsOf(obj)
	}
	return nil, false
}

// Writes: both shapes carry the label in the create call. An existing
// metadata-block object is labelled by a plan-then-apply confined to
// metadata[0].labels ([markers.WithLabels]); a manifest-declared one by a
// label merge patch to the API server ([kubesweep.LabelPatcher]), as ruled
// on #1109 and #1104.
func (kubernetes) Writes(surface markers.Surface) Writes {
	switch surface {
	case markers.SurfaceLabels:
		return Writes{Create: WriteInCreate, Adopt: WriteLabelsPlan, PostCreate: WriteNeverNeeded}
	case markers.SurfaceManifest:
		return Writes{Create: WriteInCreate, Adopt: WriteAPIPatch, PostCreate: WriteNeverNeeded}
	}
	return Writes{}
}

func (kubernetes) CarriesAddress() bool { return false }

func (kubernetes) Sweep() Sweep { return SweepLabelList }

// NewSweeper is the cluster client the provider block's own connection
// arguments build ([KubernetesSweepAttrs] mirrors hashicorp/kubernetes'
// precedence).
// On an error the client is a nil [Sweeper], never a [LabelListSweeper]
// holding a nil cluster client.
func (kubernetes) NewSweeper(providerConfig cty.Value, ok bool) (Sweeper, error) {
	cfg, err := kubesweep.RestConfig(KubernetesSweepAttrs(providerConfig, ok))
	if err != nil {
		return nil, err
	}
	client, err := kubesweep.New(cfg)
	if err != nil {
		return nil, err
	}
	return LabelListSweeper{Client: client}, nil
}

// KubernetesSweepAttrs reads the connection arguments the Kubernetes sweep
// understands off the evaluated provider block. A marked value is left
// unread rather than unmarked - the same rule internal/command's
// statelessProviders.region applies to a sensitive region - EXCEPT for the
// three arguments that are themselves the credential, which are unmarked
// and read: see secret below, and GitHub issue #1527 for what leaving them
// unread cost. It moved here from internal/command with the sweep-client
// choice (GitHub issue #1118).
func KubernetesSweepAttrs(val cty.Value, ok bool) kubesweep.Attrs {
	var a kubesweep.Attrs
	if !ok || val == cty.NilVal || val.IsNull() || !val.IsKnown() || !val.Type().IsObjectType() {
		return a
	}
	str := func(name string) string {
		if !val.Type().HasAttribute(name) {
			return ""
		}
		v := val.GetAttr(name)
		if v.IsMarked() || v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
			return ""
		}
		return v.AsString()
	}
	boolean := func(name string) bool {
		if !val.Type().HasAttribute(name) {
			return false
		}
		v := val.GetAttr(name)
		if v.IsMarked() || v.IsNull() || !v.IsKnown() || v.Type() != cty.Bool {
			return false
		}
		return v.True()
	}
	// secret is str for the three arguments that ARE the credential
	// (GitHub issue #1527). They are read whether or not they are marked,
	// because every ordinary way of supplying one marks it: an EKS root
	// takes its bearer token from data.aws_eks_cluster_auth, whose token
	// attribute the AWS provider declares sensitive, and a client
	// certificate and key come from `sensitive = true` variables. Left
	// unread, the sweep dialled the cluster anonymously and every
	// kubernetes_* type read LIST_FAILED under "the cluster refused an
	// anonymous request and this provider configuration supplies no
	// credential" - while the provider itself, handed the same value over
	// RPC and unmarked by internal/plugins/provider.go, read the cluster.
	//
	// Unmarked rather than refused, which is where this parts company
	// with internal/command's statelessProviders.region rule (see its own
	// comment): a region becomes an operator-facing hint string and a
	// secret does not belong in one, while these three go into a restclient.Config and out over
	// TLS. Nothing renders them - [kubesweep.Credentials] deliberately
	// keeps only the exec COMMAND for its messages, never a credential.
	//
	// The exec block is the one credential surface still dropped on a
	// mark, pinned by internal/command's
	// TestKubernetesSweepAttrsDropsAMarkedExecBlock; its command reaches a
	// diagnostic, so it is a separate ruling.
	secret := func(name string) string {
		if !val.Type().HasAttribute(name) {
			return ""
		}
		v, _ := val.GetAttr(name).Unmark()
		if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
			return ""
		}
		return v.AsString()
	}
	a.InCluster = boolean("in_cluster_config")
	a.ConfigPath = str("config_path")
	a.ConfigContext = str("config_context")
	a.ConfigContextAuthInfo = str("config_context_auth_info")
	a.ConfigContextCluster = str("config_context_cluster")
	a.Host = str("host")
	a.Token = secret("token")
	a.Insecure = boolean("insecure")
	a.ClusterCACertificate = str("cluster_ca_certificate")
	a.ClientCertificate = secret("client_certificate")
	a.ClientKey = secret("client_key")
	a.Exec = sweepExec(val)
	if val.Type().HasAttribute("config_paths") {
		v := val.GetAttr("config_paths")
		if !v.IsMarked() && !v.IsNull() && v.IsKnown() && v.CanIterateElements() {
			for it := v.ElementIterator(); it.Next(); {
				_, e := it.Element()
				if !e.IsMarked() && !e.IsNull() && e.IsKnown() && e.Type() == cty.String {
					a.ConfigPaths = append(a.ConfigPaths, e.AsString())
				}
			}
		}
	}
	return a
}

// sweepExec reads the provider block's exec block, which is how
// every EKS root authenticates and which nothing here read before GitHub
// issue #1114: host and cluster_ca_certificate come off
// data.aws_eks_cluster and the bearer token comes from `aws eks
// get-token` run per request. The block is a nested block of at most one,
// so the attribute is a list or a tuple of one object; nil when the
// provider block declares none.
//
// A marked argument is left unread, the same rule the scalar arguments
// follow: a command or an argument list from a sensitive variable falls
// back to whatever else the block supplies rather than being unmarked
// here. A marked command means no exec block at all, since a plugin with
// no command cannot run; a marked entry inside args or env would silently
// change what the plugin is asked to do, so a mark anywhere in either
// drops the whole block rather than running the plugin with a hole in its
// arguments.
func sweepExec(val cty.Value) *kubesweep.ExecCredential {
	if !val.Type().HasAttribute("exec") {
		return nil
	}
	blocks := val.GetAttr("exec")
	if blocks.IsMarked() || blocks.IsNull() || !blocks.IsKnown() || !blocks.CanIterateElements() {
		return nil
	}
	for it := blocks.ElementIterator(); it.Next(); {
		_, block := it.Element()
		if block.IsMarked() || block.IsNull() || !block.IsKnown() || !block.Type().IsObjectType() {
			continue
		}
		e, ok := sweepExecBlock(block)
		if !ok {
			continue
		}
		return e
	}
	return nil
}

// sweepExecBlock is one exec block. ok is false when the block
// names no command, which is the only argument without which the plugin
// cannot be run at all, or when any part of it is marked.
func sweepExecBlock(block cty.Value) (*kubesweep.ExecCredential, bool) {
	str := func(name string) (string, bool) {
		if !block.Type().HasAttribute(name) {
			return "", true
		}
		v := block.GetAttr(name)
		if v.IsNull() || !v.IsKnown() {
			return "", true
		}
		if v.IsMarked() || v.Type() != cty.String {
			return "", false
		}
		return v.AsString(), true
	}
	command, ok := str("command")
	if !ok || command == "" {
		return nil, false
	}
	apiVersion, ok := str("api_version")
	if !ok {
		return nil, false
	}
	e := &kubesweep.ExecCredential{APIVersion: apiVersion, Command: command}
	if block.Type().HasAttribute("args") {
		v := block.GetAttr("args")
		if v.IsMarked() {
			return nil, false
		}
		if !v.IsNull() && v.IsKnown() && v.CanIterateElements() {
			for it := v.ElementIterator(); it.Next(); {
				_, a := it.Element()
				if a.IsMarked() || a.IsNull() || !a.IsKnown() || a.Type() != cty.String {
					return nil, false
				}
				e.Args = append(e.Args, a.AsString())
			}
		}
	}
	if block.Type().HasAttribute("env") {
		v := block.GetAttr("env")
		if v.IsMarked() {
			return nil, false
		}
		if !v.IsNull() && v.IsKnown() && v.CanIterateElements() {
			e.Env = map[string]string{}
			for it := v.ElementIterator(); it.Next(); {
				k, a := it.Element()
				if k.IsMarked() || k.IsNull() || !k.IsKnown() || k.Type() != cty.String {
					return nil, false
				}
				if a.IsMarked() || a.IsNull() || !a.IsKnown() || a.Type() != cty.String {
					return nil, false
				}
				e.Env[k.AsString()] = a.AsString()
			}
		}
	}
	return e, true
}

// ---- GitHub issue #1584: the answers the projection's shadow enum held ----

// CreateCollidesOnKey is true on the label surface alone, and each
// exclusion is deliberate.
//
//   - SurfaceLabels is every built-in Kubernetes type whose schema carries
//     metadata[0].labels: ConfigMap, Secret, Deployment, Service,
//     Namespace, ServiceAccount, the RBAC kinds and the rest. The API
//     server stores each object under (group, resource, namespace, name) -
//     or (group, resource, name) for a cluster-scoped kind - and answers a
//     create at a key already held with 409 AlreadyExists. The provider's
//     import id for these types is that namespace/name (or name), and it
//     comes from the block's own metadata.name and metadata.namespace:
//     metadata.generate_name, the one way a Kubernetes create does not
//     name its key, is refused by internal/live/lint before a plan runs
//     (#1064). The types that patch an object someone else created
//     (kubernetes_labels, kubernetes_annotations,
//     kubernetes_config_map_v1_data, kubernetes_env, kubernetes_node_taint)
//     have no metadata.labels in their schema, so they carry no surface and
//     never reach this question. Nor does kubernetes_default_service_account,
//     whose create adopts the existing object: the projection's
//     adoptsOnCreate is checked beside this.
//   - SurfaceManifest has the same key, and already has the server's own
//     answer at plan time: the dry run (#1081,
//     discovery.DryRunKubernetesManifests) submits the planned create with
//     dryRun=All and turns the 409 into an Error quoting the server. A
//     second refusal ahead of it would stop the plan before the dry run
//     runs and replace the server's words with this tool's.
func (kubernetes) CreateCollidesOnKey(surface markers.Surface) bool {
	switch surface {
	case markers.SurfaceLabels:
		return true
	case markers.SurfaceManifest:
		return false
	}
	return false
}

// CarrierPhrase names the labels map on each shape.
func (kubernetes) CarrierPhrase(surface markers.Surface) string {
	switch surface {
	case markers.SurfaceLabels:
		return markers.LabelSurfaceBlock + "." + markers.LabelSurfaceAttr + " map"
	case markers.SurfaceManifest:
		return "manifest." + markers.LabelSurfaceBlock + "." + markers.LabelSurfaceAttr + " map"
	}
	return ""
}

// NotACarrier: a Kubernetes type with neither of the family's surfaces,
// such as the patch types (kubernetes_labels,
// kubernetes_config_map_v1_data), has nowhere of its own to carry the
// estate label. It names both carriers it lacks, and never says "tags":
// that is the AWS word, and a Kubernetes reader has no tags map to look
// for.
func (k kubernetes) NotACarrier(_ *configschema.Block, typeName string) string {
	return fmt.Sprintf(
		"%s has no %s and no %s, so there is nowhere on it to carry an ownership marker.",
		typeName, k.CarrierPhrase(markers.SurfaceLabels), k.CarrierPhrase(markers.SurfaceManifest))
}

// ---- GitHub issue #1587: the post-create marker write ----

// MarkerWriter is [WriteNeverNeeded]: the label rides the create call on
// both surfaces, so there is nothing to write after it.
func (kubernetes) MarkerWriter(addrs.AbsProviderConfig) Write { return WriteNeverNeeded }

// ---- GitHub issue #1642: whether a create needs the post-create write ----

// PostCreateNeeded is never: both surfaces carry the label in the create
// call ([WriteNeverNeeded]).
func (kubernetes) PostCreateNeeded(markers.Surface, string, CreateTagFacts) (string, bool) {
	return "", false
}

// ---- GitHub issue #1653: the manual-mark hint ----

// ManualMarkFix is never asked: PostCreateNeeded above never answers true,
// so no Kubernetes create ever fails a post-create write that would need
// this. Implemented for [Substrate] completeness with the same generic
// sentence the package-level [ManualMarkFix] gives a surface with no
// family at all.
func (kubernetes) ManualMarkFix(_, _ string, want map[string]string, _ CreateTagFacts) string {
	return fmt.Sprintf("Mark it by hand with the markers %s, then plan again.", markers.TagsArgument(want))
}

// ---- GitHub issue #1649: the carrier's wholly-known read ----

// CarrierPaths: metadata[0].labels, and manifest.metadata.labels. The label
// map alone, never the whole metadata block: a planned object's
// resource_version or generation can be unknown while its labels are not.
func (kubernetes) CarrierPaths(surface markers.Surface) []cty.Path {
	switch surface {
	case markers.SurfaceLabels:
		return []cty.Path{cty.GetAttrPath(markers.LabelSurfaceBlock).IndexInt(0).GetAttr(markers.LabelSurfaceAttr)}
	case markers.SurfaceManifest:
		return []cty.Path{cty.GetAttrPath(markers.ManifestSurfaceAttr).GetAttr(markers.LabelSurfaceBlock).GetAttr(markers.LabelSurfaceAttr)}
	}
	return nil
}

func (kubernetes) MarkerNoun(surface markers.Surface) string {
	switch surface {
	case markers.SurfaceLabels, markers.SurfaceManifest:
		return "label"
	}
	return ""
}
