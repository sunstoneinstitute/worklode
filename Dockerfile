# Multi-stage build: compile the static lode binary, then ship it on a minimal,
# non-root distroless base with no shell and no package manager.

FROM golang:1.27 AS build
WORKDIR /src

# Cache mounts persist module downloads and build cache across runs. A
# self-hosted CI runner keeps them in a builder whose state volume survives the
# job; a hosted one syncs them to actions/cache via buildkit-cache-dance.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /lode-server ./cmd/lode-server && \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /lode-migrate ./cmd/lode-migrate && \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /lode-watch ./cmd/lode-watch

# ffmpeg extracts the first frame of an uploaded video as its poster image
# (spec 021 §5), so an embedded <video> is a picture of the bug rather than a
# black rectangle. It arrives as one statically linked binary copied out of an
# upstream image rather than as an apt install: a distro package would mean
# giving up the distroless base — no shell, no package manager, nothing else
# with a CVE feed — for a garnish. The server treats it as optional at
# runtime, so an image without it still serves videos, just without posters.
FROM mwader/static-ffmpeg:9.0 AS ffmpeg

FROM gcr.io/distroless/static-debian12:nonroot AS watcher
COPY --from=build /lode-watch /lode-watch
ENTRYPOINT ["/lode-watch"]

FROM gcr.io/distroless/static-debian12:nonroot AS server
COPY --from=build /lode-server /lode-server
COPY --from=build /lode-migrate /lode-migrate
COPY --from=ffmpeg /ffmpeg /usr/local/bin/ffmpeg
COPY deploy/base/migrations /migrations
# Stated rather than inherited: the server finds ffmpeg by looking it up on
# PATH, and a base image that quietly stopped setting one would turn every
# poster into a silent "unavailable" rather than into a build failure.
ENV PATH=/usr/local/bin:/usr/bin:/bin

EXPOSE 8080
ENTRYPOINT ["/lode-server"]
