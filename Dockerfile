# Build stage: CGO is required by the tree-sitter bindings, so this mirrors
# the goreleaser build (CGO_ENABLED=1) rather than a static/scratch image.
FROM golang:1.22-bookworm AS builder
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/max-context ./cmd/max-context

# Runtime: glibc base (CGO binary) plus git, which get_impact --from-git
# shells out to.
FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends git ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 10001 maxcontext
COPY --from=builder /out/max-context /usr/local/bin/max-context
USER maxcontext
WORKDIR /workspace

# Usage:
#   docker run -i --rm -v "$PWD":/workspace <image>                 # MCP server (stdio)
#   docker run    --rm -v "$PWD":/workspace <image> --index         # build the index
#   docker run    --rm -v "$PWD":/workspace <image> query "term"    # one-shot CLI
ENTRYPOINT ["max-context"]
