// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"fmt"

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

func ParseLiveCluster(args []string) (*LiveCluster, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	lc := &LiveCluster{}

	cmdFlags := defaultFlagSet("live-cluster")
	cmdFlags.StringVar(&lc.Namespace, "namespace", "", "namespace")
	cmdFlags.StringVar(&lc.Estate, "estate", "", "estate")
	cmdFlags.BoolVar(&lc.PlanIdentity, "plan-identity", false, "plan-identity")
	cmdFlags.BoolVar(&lc.JSON, "json", false, "json")

	if err := cmdFlags.Parse(args); err != nil {
		return lc, diags.Append(tfdiags.Sourceless(tfdiags.Error, "Invalid option", fmt.Sprintf("%s.", err)))
	}
	if len(cmdFlags.Args()) != 0 {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Too many arguments",
			"live-cluster takes no positional arguments. Run it in a configuration directory, or name a namespace with -namespace=<name>."))
	}
	return lc, diags
}
