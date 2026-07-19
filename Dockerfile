# Multi-stage build: compile the static wt binary, then ship it on a minimal,
# non-root distroless base with no shell and no package manager.

FROM golang:1.25 AS build
WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /wt ./cmd/wt

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /wt /wt

EXPOSE 8080
ENTRYPOINT ["/wt"]
CMD ["serve", "--db", "/data/wt.db", "--listen", ":8080"]
