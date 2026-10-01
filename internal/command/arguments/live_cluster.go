// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveCluster is the parsed command line of "choudoufu live-cluster".
type LiveCluster struct {
	// Namespace names the records namespace to report on. Empty means "the
	// one this directory's configuration resolves to", which is how an
	// operator checks the namespace an estate actually uses.
	//
	// Set, it overrides the namespace and nothing else (GitHub issue
	// #1448). A record_store "kubernetes" block in this directory still
	// says which cluster is reached, because a report about some other
	// cluster's namespace of the same name is a report about nothing.
	// Outside a configuration directory there is no such block and the
	// ambient kubeconfig is reached, which is how a cluster admin checks a
	// namespace before any estate exists.
	Namespace string

	// Estate is the estate whose records live there. With -namespace it is
	// only reported; without one it is what the default namespace name is
	// derived from.
	Estate string

	// PlanIdentity asks the question a CI plan job's identity would ask:
	// does it have what a PLAN needs, which is get and list and not the
	// three write verbs. Without it the question is what an apply needs.
	PlanIdentity bool

	// JSON asks for one JSON document on stdout instead of the table.
	JSON bool
}

// BindLiveCluster registers live-cluster's options on cli. See
// [BindLiveBucket] for why -json is the command's own.
func BindLiveCluster(cli *CommandLine) *LiveCluster {
	lc := &LiveCluster{}
	BindView(cli, viewFlagNone)

	cli.StringVar(&lc.Namespace, "namespace", "", "The records namespace to check, instead of the one the configuration resolves to.").SetDisplay("=name")
	cli.StringVar(&lc.Estate, "estate", "", "The estate whose records live there.").SetDisplay("=name")
	cli.BoolVar(&lc.PlanIdentity, "plan-identity", false, "Ask what a PLAN job's identity needs instead of what an apply needs.")
	cli.BoolVar(&lc.JSON, "json", false, "One JSON document on stdout.")

	var rest []string
	cli.VariadicArg(&rest, "ARGS")

	cli.PreHook(func() tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		if len(rest) != 0 {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Too many arguments",
				"live-cluster takes no positional arguments. Run it in a configuration directory, or name a namespace with -namespace=<name>."))
		}
		return diags
	})
	return lc
}

// ParseLiveCluster processes CLI arguments through [BindLiveCluster].
func ParseLiveCluster(args []string) (*LiveCluster, tfdiags.Diagnostics) {
	cli := new(CommandLine)
	lc := BindLiveCluster(cli)
	closer, diags := cli.parseWithHooks("live-cluster", args)
	closer()
	return lc, diags
}
