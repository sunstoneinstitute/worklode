# Image for worklode agents: a Claude Code or Codex session working this
# repo's checkout (running `lode next`/`lode done` etc. against the
# backbone). Build context is the repo root:
#
#   docker build -f .worklode/Dockerfile -t worklode-agent .
#
# This precedes spec 038's docker/base + docker/generic split (038 §3.1) —
# neither that family nor scripts/bootstrap.sh exists yet. This file is a
# standalone image for now; fold it into docker/generic once those land,
# per 038 §3's "an image whose contents disagree with bootstrap.sh is a
# defect in the image."

FROM golang:1.26

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      git \
      jq \
      pipx \
      xz-utils \
    && rm -rf /var/lib/apt/lists/*

ARG AGENT_UID=1000
ARG AGENT_GID=1000
RUN groupadd -g "$AGENT_GID" agent \
    && useradd -m -u "$AGENT_UID" -g "$AGENT_GID" -s /bin/bash agent

# Node.js: a pinned tarball, not Debian's apt package (too old for the 22+
# plugins/obsidian/ needs — CLAUDE.md), and not NodeSource's apt repo (one fewer
# trusted key to manage). Same pin-plus-checksum shape as
# scripts/tailwind.sha256, table keyed by TARGETARCH so this builds on both
# amd64 and arm64 under buildx.
ARG NODE_VERSION=22.23.2
ARG TARGETARCH
RUN set -eux; \
    case "$TARGETARCH" in \
      amd64) node_arch=x64; sha256=d60acfe00a2932254bb0ad20e01b0d74397a0875595de719654b214f4b03f307 ;; \
      arm64) node_arch=arm64; sha256=fff4078c5def658577f92c88db7db3bc0072924bfb93fe52c1e744a54e94abb8 ;; \
      *) echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${node_arch}.tar.xz" -o /tmp/node.tar.xz; \
    echo "${sha256}  /tmp/node.tar.xz" | sha256sum -c -; \
    tar -xJf /tmp/node.tar.xz -C /usr/local --strip-components=1; \
    rm /tmp/node.tar.xz
RUN corepack enable pnpm

# gh: also a pinned tarball, for the same reason Node is — Debian's apt
# package tracks unstable/testing, not bookworm.
ARG GH_VERSION=2.97.0
RUN set -eux; \
    case "$TARGETARCH" in \
      amd64) gh_arch=amd64; sha256=a2c9b8497e1f85b1ad0dfcb78b5a622e098801b8e461e459e88e1ee12f018112 ;; \
      arm64) gh_arch=arm64; sha256=73ea440ecad9c9e284429997ee6f93577bc6f7bc6fba357ef62c53ad8fb641a5 ;; \
      *) echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${gh_arch}.tar.gz" -o /tmp/gh.tar.gz; \
    echo "${sha256}  /tmp/gh.tar.gz" | sha256sum -c -; \
    tar -xzf /tmp/gh.tar.gz -C /tmp; \
    install /tmp/gh_${GH_VERSION}_linux_${gh_arch}/bin/gh /usr/local/bin/gh; \
    rm -rf /tmp/gh.tar.gz /tmp/gh_${GH_VERSION}_linux_${gh_arch}

# pre-commit, via pipx rather than pip: Debian marks the system Python
# externally-managed (PEP 668), so a bare `pip install` fails there.
# PIPX_BIN_DIR puts the shim straight on the default PATH, so this works
# for any user without each one needing `pipx ensurepath`.
ENV PIPX_HOME=/opt/pipx PIPX_BIN_DIR=/usr/local/bin
RUN pipx install pre-commit

# Pre-warm the module cache so a checked-out worktree's first `go build`/`go
# test`/`go vet` doesn't pay for every dependency over the network. No
# --mount=type=cache here, unlike the root Dockerfile's build stage: that
# would leave the download outside this image's layers, defeating the
# point. /go is world-writable (sticky bit, like /tmp) in the base image, so
# the agent user can still add modules go.sum doesn't yet cover.
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
WORKDIR /

# Claude Code and Codex CLI: intentionally unpinned. Unlike Node/gh, both
# ship multiple releases a week; pinning here would mean this file going
# stale by design rather than by neglect, with no bump mechanism to catch
# it (contrast Node/gh/tailwind.sha256, which do have one).
RUN npm install -g @anthropic-ai/claude-code @openai/codex

USER agent
WORKDIR /home/agent
