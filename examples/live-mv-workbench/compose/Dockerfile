# Stage 1: build choudoufu from this checkout's source (no release download,
# no auth) so the demo container carries a binary that matches the code.
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/choudoufu ./cmd/choudoufu

# Stage 2: marimo serving the demo, pointed at floci by the compose env.
FROM python:3.13-slim-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv
COPY --from=build /out/choudoufu /usr/local/bin/choudoufu
WORKDIR /app
COPY examples/live-mv-workbench /app
RUN uv sync --extra viz
EXPOSE 2718
CMD ["uv", "run", "marimo", "run", "demo.py", "--host", "0.0.0.0", "--port", "2718", "--no-token"]
