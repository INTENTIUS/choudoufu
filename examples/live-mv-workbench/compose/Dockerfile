# Which of the two stages below becomes the binary this image ships.
# "release" (the default) needs nothing but Docker: it downloads the same
# pinned build the docs measure against, the way a first-time user runs the
# demo. "source" (BUILD_MODE=source) builds this checkout's own code
# instead, for developing the engine and the demo together; it needs the
# full repo as build context, not just this example, and pulls a Go
# toolchain. ARG used in a later FROM must be declared before the first
# FROM, so this sits above both stages.
ARG BUILD_MODE=release

# Stage 1a: download the pinned release choudoufu is measured against. No
# Go toolchain, no repo source beyond this example -- the default path.
# Asset names and the download shape mirror tlmig/localbuild.py's
# fetch_release(), which does the same thing for a host-side run.
FROM debian:bookworm-slim AS build-release
ARG CHOUDOUFU_VERSION=v0.10.1
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*
RUN ARCH="$(dpkg --print-architecture)" \
    && curl -fsSL "https://github.com/INTENTIUS/choudoufu/releases/download/${CHOUDOUFU_VERSION}/choudoufu_${CHOUDOUFU_VERSION}_linux_${ARCH}.tar.gz" -o /tmp/choudoufu.tar.gz \
    && mkdir -p /out && tar -xzf /tmp/choudoufu.tar.gz -C /out choudoufu \
    && rm /tmp/choudoufu.tar.gz && chmod +x /out/choudoufu

# Stage 1b: build choudoufu from this checkout's source (opt-in). .dockerignore
# excludes .git from the build context, so `git describe` cannot run in
# here; FORK_LABEL is the host's describe, passed in by the justfile.
# Without the link-time stamp, `choudoufu version` reports itself as plain
# OpenTofu and preflight refuses it as "not a choudoufu binary" -- the same
# stamp tlmig/localbuild.py applies for a host-side local build.
FROM golang:1.26-bookworm AS build-source
ARG FORK_LABEL=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-X github.com/intentius/choudoufu/version.Fork=${FORK_LABEL}" -o /out/choudoufu ./cmd/choudoufu

# BuildKit only executes the stage BUILD_MODE names -- the other one is
# never built, so picking "release" costs nothing toward a Go toolchain
# and picking "source" costs nothing toward a release download.
FROM build-${BUILD_MODE} AS build

# Stage 2: marimo serving the demo, pointed at floci by the compose env.
FROM python:3.13-slim-bookworm
# tlmig shells out to the aws CLI directly (tagging lookups, CloudTrail, IAM
# reads for the receipt and teardown beats); it is not a Python dependency
# uv sync pulls in, so it needs its own install. The official installer
# (not the apt package) so both amd64 and arm64 hosts get the same binary.
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates unzip \
    && ARCH="$(dpkg --print-architecture)" \
    && case "$ARCH" in arm64) AWSARCH=aarch64 ;; *) AWSARCH=x86_64 ;; esac \
    && curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-${AWSARCH}.zip" -o /tmp/awscliv2.zip \
    && unzip -q /tmp/awscliv2.zip -d /tmp && /tmp/aws/install \
    && rm -rf /tmp/awscliv2.zip /tmp/aws \
    && rm -rf /var/lib/apt/lists/*
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv
COPY --from=build /out/choudoufu /usr/local/bin/choudoufu
WORKDIR /app
COPY examples/live-mv-workbench /app
RUN uv sync --extra viz
EXPOSE 2718
CMD ["uv", "run", "marimo", "run", "demo.py", "--host", "0.0.0.0", "--port", "2718", "--no-token"]
