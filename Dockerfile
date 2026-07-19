# Multi-stage build: compile the static wt binary, then ship it on a minimal,
# non-root distroless base with no shell and no package manager.

FROM golang:1.25 AS build
WORKDIR /src

# Cache mounts persist module downloads and build cache across runs;
# in CI they are synced to actions/cache via buildkit-cache-dance.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /wt ./cmd/wt

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /wt /wt

EXPOSE 8080
ENTRYPOINT ["/wt"]
CMD ["serve", "--db", "/data/wt.db", "--listen", ":8080"]
