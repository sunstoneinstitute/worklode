# Multi-stage build: compile the static lode binary, then ship it on a minimal,
# non-root distroless base with no shell and no package manager.

FROM golang:1.26 AS build
WORKDIR /src

# Cache mounts persist module downloads and build cache across runs;
# in CI they are synced to actions/cache via buildkit-cache-dance.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /lode ./cmd/lode

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /lode /lode

EXPOSE 8080
ENTRYPOINT ["/lode"]
CMD ["serve", "--listen", ":8080"]
