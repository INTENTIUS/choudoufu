# OpenTofu Tracing Guide

This document describes how to use and implement tracing in OpenTofu Core using OpenTelemetry.

There's background information on OpenTofu's tracing implementation in [the OpenTelemetry Tracing RFC](https://github.com/opentofu/opentofu/blob/main/rfc/20250129-Tracing-For-Extra-Context.md)

> [!WARNING]
> If you change which version of the `go.opentelemetry.io/otel/sdk` we have selected in our `go.mod`, you **must** make sure that `internal/tracing/traceattrs/semconv.go` imports the same subpackage of `go.opentelemetry.io/otel/semconv/*` that is used by the selected version of `go.opentelemetry.io/otel/sdk`.
>
> This is important because our tracing setup uses a blend of directly-constructed `semconv` attributes and attributes chosen indirectly through the `resource` package, and they must all be using the same version of the semantic conventions schema or there will be a "conflicting Schema URL" error at runtime.
>
> (Problems of this sort should be detected both by a unit test in `internal/tracing/traceattrs` and an end-to-end test that executes OpenTofu with tracing enabled.)

## Overview

OpenTofu provides distributed tracing capabilities via OpenTelemetry to help end users understand the execution flow and performance characteristics of OpenTofu operations. Tracing is particularly useful for:

- Debugging performance issues (e.g., "Why is my plan taking so long?")
- Understanding time spent in different operations
- Visualizing the execution flow across providers and modules
- Diagnosing issues in CI/CD pipelines

Tracing in OpenTofu is **strictly opt-in** and disabled by default. It's designed to have minimal overhead when disabled and to provide valuable insights when enabled.

> [!IMPORTANT]  
> OpenTofu's tracing functionality refers only to OpenTelemetry traces for local debugging and analysis.
> No telemetry or usage data is sent to external servers, and no data leaves your environment unless you explicitly configure an external collector.

## Enabling Tracing

To enable tracing in OpenTofu:

1. Set the environment variable `OTEL_TRACES_EXPORTER=otlp`
2. Configure the OpenTelemetry exporter using standard OpenTelemetry environment variables

Example configuration for a local Jaeger collector:

```bash
export OTEL_TRACES_EXPORTER=otlp
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
export OTEL_EXPORTER_OTLP_INSECURE=true
```

For a complete list of configuration options, refer to the [OpenTelemetry Documentation](https://opentelemetry.io/docs/specs/otel/protocol/exporter/).

## Joining a caller's trace

When `TRACEPARENT` (and optionally `TRACESTATE`) is set, every span of the run
nests under that parent, so a wrapper such as terragucci sees one trace per
root. choudoufu hands its own spans on in turn:

- each provider process is started with `TRACEPARENT` set to its
  `Start provider` span, replacing any `TRACEPARENT` inherited from the caller;
- each provider gRPC call carries its span as `traceparent` in the gRPC
  metadata, which a provider instrumented with otelgrpc reads;
- each child process of `live-plan-set` and `live-wave-apply` gets the span of
  the stage that runs it.

## Detail spans and the span budget

Some spans come one per resource instance or one per provider resource call,
so a large estate makes thousands of them. These detail spans are governed by
`CHOUDOUFU_TRACE_DETAIL`:

| Value | Detail spans | Summary spans |
|---|---|---|
| `auto` (default) | up to `CHOUDOUFU_TRACE_SPAN_BUDGET` per graph walk (default 2000), then counted only | written only when something was counted without its own span |
| `full` | all of them | none |
| `aggregate` | none | always |

A summary span is named `Aggregate: <detail span name>` and sits under the
`Graph walk` span. There is one per resource type and phase, or per provider
and RPC method. Each carries `choudoufu.aggregate.kind`, `.count`,
`.detailed_count`, `.duration_total_ms`, `.duration_max_ms` and `.slowest`
(the slowest member's address or resource type), plus `opentofu.resource.type`
or `opentofu.provider.address` and `rpc.method`. A walk writes at most 256
summary spans; any further groups are folded into one `Aggregate: other` span.

So the budget per graph walk is: in `auto`, at most the budget's detail spans
plus at most 256 summaries; in `aggregate`, at most 256 summaries; `full` has
no bound.

## Span reference

None of these spans carry resource attribute values. They hold addresses,
actions, type names, method names and counts.

| Span | Under | Attributes |
|---|---|---|
| `Graph walk` | `Plan phase`, `Apply phase`, ... | `opentofu.walk.operation` |
| `Plan resource instance changes` (detail) | `Graph walk` | `opentofu.resource_instance.address`, `opentofu.resource.type`, `opentofu.resource_instance.action`, `opentofu.provider_instance.address` |
| `Refresh resource instance` (detail) | `Plan resource instance changes` | |
| `Apply resource instance changes` (detail) | `Graph walk` | as for plan |
| `tfplugin5.Provider/<Method>`, `tfplugin6.Provider/<Method>` | the resource span, or `Graph walk` | `rpc.system`, `rpc.service`, `rpc.method`, `opentofu.provider.address`, `opentofu.resource.type` |
| `Start provider` | whatever started it | `opentofu.provider.address` |
| `State lock wait` | the operation | `opentofu.state.backend`, `opentofu.state.lock.operation`, `opentofu.state.lock.attempts`, `opentofu.state.lock.id` |
| `State read`, `State write`, `State unlock` | the operation | `opentofu.state.backend` |
| `live-plan-set` | `tofu` | `choudoufu.roots`, `choudoufu.exit_code` |
| `Set plan root`, `Set plan stage`, `Set digest` | `live-plan-set` | `choudoufu.root`, `choudoufu.estate`, `choudoufu.stage`, `choudoufu.status`, `choudoufu.set.digest` |
| `live-wave-apply` | `tofu` | `choudoufu.wave.number`, `choudoufu.exit_code` |
| `Wave set digest`, `Wave resume check`, `Wave fresh plan`, `Wave apply root` | `live-wave-apply` | `choudoufu.refused`, `choudoufu.refused.step`, `choudoufu.refused.reason`, `choudoufu.root`, `choudoufu.wave.outcome` |

Provider calls that go through the detail budget are `ReadResource`,
`PlanResourceChange`, `ApplyResourceChange`, `ReadDataSource` and
`ImportResourceState`. `GetProviderSchema` and `ConfigureProvider` always get
a span.

## Quick Start with Jaeger

To quickly spin up a local Jaeger instance with OTLP support:

```bash
docker run -d --rm --name jaeger \
  -p 16686:16686 \
  -p 4317:4317 \
  -p 4318:4318 \
  -p 5778:5778 \
  -p 9411:9411 \
  jaegertracing/jaeger:2.5.0
```

Then configure OpenTofu as shown above and access the Jaeger UI at http://localhost:16686.

## Adding Tracing to OpenTofu Code

> [!NOTE]  
> **For Contributors**: When adding tracing to OpenTofu, remember that the primary audience is **end users** who need to understand performance, not OpenTofu developers. Add spans sparingly to avoid polluting traces with too much detail.

### Basic Span Creation

```go
import (
    "github.com/opentofu/opentofu/internal/tracing"
    "github.com/opentofu/opentofu/internal/tracing/traceattrs"
)

func SomeFunction(ctx context.Context) error {
    // Create a new span
    ctx, span := tracing.Tracer().Start(ctx, "Human readable operation name",
        tracing.SpanAttributes(
            traceattrs.String("opentofu.some_attribute", "value")
        ),
    )
    defer span.End()
    
    // Optionally add additional attributes after the span is created, if
    // they only need to appear in certain cases.
    span.SetAttributes(traceattrs.String("opentofu.some_other_attribute", "value"))
    
    // Use the more specific attribute-construction helpers from package
    // traceattrs where they are relevant, to ensure we follow consistent
    // semantic conventions for cross-cutting concerns.
    span.SetAttributes(traceattrs.OpenTofuProviderAddress("hashicorp/aws"))
    
    // Your function logic here...
    
    // If an error occurs
    if err != nil {
        tracing.SetSpanError(span, err)
        return err
    }
    
    return nil
}
```

OpenTelemetry has many different packages spread across a variety of different Go modules, and those different modules often need to be upgraded together to ensure consistent behavior and avoid errors at runtime.

Therefore we prefer to directly import `go.opentelemetry.io/otel/*` packages only from our packages under `internal/tracing`, and then reexport certain functions from our own packages so that we can manage all of the OpenTelemetry dependencies in a centralized place to minimize "dependency hell" problems when upgrading. Packages under `go.opentelemetry.io/contrib/instrumentation/*` are an exception because they tend to be more tightly-coupled to whatever they are instrumenting than to the other OpenTelemetry packages, and so it's better to import those from the same file that's importing whatever other package the instrumentation is being applied to.

> [!WARNING]
> Don't import `go.opentelemetry.io/otel/semconv/*` packages from anywhere except `internal/tracing/traceattrs/semconv.go`!
>
> If you want to use standard OpenTelemetry semantic conventions from other packages, use them indirectly through reexports in `package traceattrs` instead, so we can make sure there's only one file in OpenTofu deciding which version of semconv we are currently depending on.

### Tracing Conventions

#### Span Naming

- Use human-readable, action-oriented names that describe operations from a user perspective
- Prefer names like "Provider installation" over internal function names like "InstallProvider"
- Use consistent terminology from the OpenTofu CLI and documentation
- Span names should represent UX-level concepts, not internal code structure

#### Attributes

- Prefer standard [OpenTelemetry semantic conventions](https://opentelemetry.io/docs/specs/semconv/) where applicable, using helper functions from [`internal/tracing/traceattrs`](https://pkg.go.dev/github.com/opentofu/opentofu/internal/tracing/traceattrs).
- Use `OpenTofu`-prefixed functions in [`internal/tracing/traceattrs`](https://pkg.go.dev/github.com/opentofu/opentofu/internal/tracing/traceattrs) for OpenTofu-specific cross-cutting concerns.
- It's okay to use one-off inline strings for attribute names specific to a single span, but make sure to still follow the [OpenTelemetry attribute naming conventions](https://opentelemetry.io/docs/specs/semconv/general/naming/) and use the `opentofu.` prefix for anything that is not a standardized semantic convention.
- If a particular subsystem of OpenTofu has some repeated conventions for attribute names, consider creating unexported string constants or attribute construction helper functions in the same package to centralize those naming conventions.

#### Error Handling

Use the `tracing.SetSpanError` helper to consistently record errors:

```go
if err != nil {
    tracing.SetSpanError(span, err)
    return err
}
```

This helper supports various error types including standard errors, strings, and OpenTofu diagnostics.

### Instrumentation Guidelines

1. **Focus on Key Operations**: Instrument high-level operations that are meaningful to end users rather than every internal function.
2. **Include Valuable Context**: Add attributes that help identify resources, modules, or operations.
3. **Respect Performance**: Avoid expensive computations solely for tracing.
