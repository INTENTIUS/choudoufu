// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package traceattrs

import (
	"go.opentelemetry.io/otel/attribute"
)

// GitHub issue #1898. Attribute names for the deeper spans: provider calls,
// per-resource-instance graph walk spans, state locks and the aggregate
// summaries that stand in for detail spans over the span budget.
//
// Every attribute here carries a name, an address, an action, a type or a
// count. None of them may carry a resource attribute value: the same rule
// terragucci's report follows.

const (
	// AttrResourceInstanceAddress matches the constant the tofu package has
	// used for its per-instance spans since before #1898.
	AttrResourceInstanceAddress = "opentofu.resource_instance.address"
	// AttrResourceInstanceAction is the planned change action ("Create",
	// "Update", "Delete", "DeleteThenCreate", "CreateThenDelete", "Read",
	// "NoOp", "Forget"), as [plans.Action.String] prints it.
	AttrResourceInstanceAction = "opentofu.resource_instance.action"
	// AttrResourceType matches the tofu package's constant of the same value.
	AttrResourceType = "opentofu.resource.type"

	AttrRPCSystem  = "rpc.system"
	AttrRPCService = "rpc.service"
	AttrRPCMethod  = "rpc.method"

	AttrStateLockID        = "opentofu.state.lock.id"
	AttrStateLockOperation = "opentofu.state.lock.operation"
	AttrStateLockAttempts  = "opentofu.state.lock.attempts"
	AttrStateBackend       = "opentofu.state.backend"

	AttrAggregateKind          = "choudoufu.aggregate.kind"
	AttrAggregateCount         = "choudoufu.aggregate.count"
	AttrAggregateDetailedCount = "choudoufu.aggregate.detailed_count"
	AttrAggregateDurationTotal = "choudoufu.aggregate.duration_total_ms"
	AttrAggregateDurationMax   = "choudoufu.aggregate.duration_max_ms"
	AttrAggregateSlowest       = "choudoufu.aggregate.slowest"
	AttrAggregateGroups        = "choudoufu.aggregate.groups"

	// The live-plan-set and live-wave-apply spans. A root is the root
	// module's directory relative to the set's base, an estate the name
	// its live block declares.
	AttrRoot      = "choudoufu.root"
	AttrEstate    = "choudoufu.estate"
	AttrStage     = "choudoufu.stage"
	AttrStatus    = "choudoufu.status"
	AttrRoots     = "choudoufu.roots"
	AttrExitCode  = "choudoufu.exit_code"
	AttrWave      = "choudoufu.wave.number"
	AttrOutcome   = "choudoufu.wave.outcome"
	AttrMoved     = "choudoufu.wave.moved"
	AttrSetDigest = "choudoufu.set.digest"

	AttrRefused       = "choudoufu.refused"
	AttrRefusedStep   = "choudoufu.refused.step"
	AttrRefusedReason = "choudoufu.refused.reason"
)

// OpenTofuResourceInstanceAddress is the address of the resource instance a
// span is about, from [addrs.AbsResourceInstance.String].
func OpenTofuResourceInstanceAddress(addr string) attribute.KeyValue {
	return attribute.String(AttrResourceInstanceAddress, addr)
}

// OpenTofuResourceInstanceAction is the planned action for the instance.
func OpenTofuResourceInstanceAction(action string) attribute.KeyValue {
	return attribute.String(AttrResourceInstanceAction, action)
}

// OpenTofuResourceType is the resource type, such as "aws_s3_bucket".
func OpenTofuResourceType(typ string) attribute.KeyValue {
	return attribute.String(AttrResourceType, typ)
}

// RPCSystem, RPCService and RPCMethod follow the OpenTelemetry RPC semantic
// conventions, so a backend that groups gRPC client spans groups ours too.
func RPCSystem(v string) attribute.KeyValue  { return attribute.String(AttrRPCSystem, v) }
func RPCService(v string) attribute.KeyValue { return attribute.String(AttrRPCService, v) }
func RPCMethod(v string) attribute.KeyValue  { return attribute.String(AttrRPCMethod, v) }

// Float64 wraps [attribute.Float64], like the wrappers in generic.go.
func Float64(name string, val float64) attribute.KeyValue {
	return attribute.Float64(name, val)
}
