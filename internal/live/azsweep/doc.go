// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package azsweep is the Azure estate sweep's client (GitHub issue #1120,
// the Azure platform epic, under #1122's ruling of 2026-10-19): which live
// resources in a set of subscriptions carry this estate's tofu-estate tag,
// so that one whose block the configuration no longer declares can be
// proposed for removal, exactly as the AWS tagging sweep, the Kubernetes
// label list (internal/live/kubesweep) and the GCP asset search
// (internal/live/gcpsweep) do on their substrates.
//
// # What a sweep is here
//
// Azure has a cross-service, tag-filtered query: Azure Resource Graph
// (POST {resourceManager}/providers/Microsoft.ResourceGraph/resources,
// api-version 2022-10-01). One query per subscription, read page by page
// through $skipToken until the response carries none, returns every
// resource and resource group of every type that carries the tag. The
// query is [Query]; its comment says why it is shaped the way it is.
//
// One subscription per query, never several in one request, is a
// correctness choice and not a convenience. Resource Graph answers a
// request naming several subscriptions from the ones the caller can read
// and leaves the rest out without saying so, so a subscription the
// credential cannot read would come back as one holding nothing tagged.
// Asked about that subscription alone, the service has to answer with an
// error ([UnreadableSubscriptionError]).
//
// # Case
//
// ARM tag keys are case-insensitive: ARM keeps the spelling the writer
// used and refuses a second key that differs from the first only by case.
// The marker is written lowercase and read case-insensitively (#1122), so
// a resource tagged Tofu-Estate by its own configuration is this estate's
// as surely as one tagged tofu-estate. Tag values are case-sensitive in
// ARM, and the estate name is compared exactly.
//
// # Failure is never an empty answer
//
// Every way a subscription's query can fail is an error, typed so that the
// discovery leg can name the fix: [NotRegisteredError] (the subscription
// has not registered Microsoft.ResourceGraph, a refusal),
// [UnreadableSubscriptionError] (the credential cannot read it, an
// incomplete sweep), [ThrottledError] (the service kept answering 429
// past the retries its own headers allow, an incomplete sweep),
// [PartialError] (a page after the first failed, so the pages already
// read are discarded), and [UncoveredSubscriptionError] (no configured
// credential covers the subscription). Each carries the diagnostic
// summary a later unit registers in internal/live/discovery/refusals.go
// ([Summary]). A 200 with no rows is the one empty answer.
//
// # Credentials
//
// [ProviderAttrs] reads an azurerm or azapi provider block's evaluated
// configuration, [Attrs.WithEnv] applies the ARM_* environment variables
// the provider itself reads for an argument the block leaves unset, and
// [New] authenticates through the same credential chain the azurerm state
// backend uses (internal/backend/remote-state/azure/auth): client
// certificate, client secret, OIDC, managed identity, AKS workload
// identity, then the Azure CLI. The credential is an
// azcore.TokenCredential, so [NewWith] takes any, and the tests hand it a
// fake token.
package azsweep
