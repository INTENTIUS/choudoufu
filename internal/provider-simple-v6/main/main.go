// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"github.com/intentius/choudoufu/internal/grpcwrap"
	plugin "github.com/intentius/choudoufu/internal/plugin6"
	simple "github.com/intentius/choudoufu/internal/provider-simple-v6"
	"github.com/intentius/choudoufu/internal/tfplugin6"
)

func main() {
	plugin.Serve(&plugin.ServeOpts{
		GRPCProviderFunc: func() tfplugin6.ProviderServer {
			return grpcwrap.Provider6(simple.Provider())
		},
	})
}
