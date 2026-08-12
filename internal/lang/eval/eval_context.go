// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package eval

import (
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/eval/internal/evalglue"
	"github.com/intentius/choudoufu/internal/lang/eval/internal/tofu2024"
	"github.com/intentius/choudoufu/internal/providers"
)

// The symbols aliased in this file are defined in [evalglue] really just to
// avoid a dependency between this package and the "compiler" packages
// like ./internal/tofu2024, but we do still need them in our exported API
// here so that other parts of OpenTofu can interact with the evaluator.

type EvalContext = evalglue.EvalContext
type Providers = evalglue.Providers
type ProvidersSchema = evalglue.ProvidersSchema
type Provisioners = evalglue.Provisioners
type ProvisionersSchema = evalglue.ProvisionersSchema
type ExternalModules = evalglue.ExternalModules
type UncompiledModule = evalglue.UncompiledModule

func ModulesForTesting(modules map[addrs.ModuleSourceLocal]*configs.Module) ExternalModules {
	// This one actually lives in tofu2024 because evalglue isn't allowed to
	// depend on tofu2024 itself, but from the caller's perspective this is
	// still presented as an evalglue re-export because the return type belongs
	// to that package.
	return tofu2024.ModulesForTesting(modules)
}

func ProvidersForTesting(schemas map[addrs.Provider]*providers.GetProviderSchemaResponse) Providers {
	return evalglue.ProvidersForTesting(schemas)
}

func ProvisionersForTesting(schemas map[string]*configschema.Block) ProvisionersSchema {
	return evalglue.ProvisionersForTesting(schemas)
}
