# syntax=docker/dockerfile:1

# Build stage: generate the templ view code, then compile a static binary. The
# cache mounts keep `docker compose watch` rebuilds fast — only changed packages
# recompile. templ is pinned as a go.mod tool, so `go generate` needs no install.
FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go generate ./... && CGO_ENABLED=0 go build -o /fortytwode .

# Runtime stage: just the static binary. The SQL migrations, style.css and
# syncing.js are go:embed-ed into it, so nothing else is copied. ca-certificates is
# needed for the outbound HTTPS calls to the 42 API.
FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /fortytwode /fortytwode
EXPOSE 4242
# ENTRYPOINT is just the binary with `serve` as the default command, so the same
# image can run one-off subcommands (e.g. `docker compose run --rm app migrate`).
ENTRYPOINT ["/fortytwode"]
CMD ["serve"]
