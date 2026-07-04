# ── Stage 1: build ──────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ ./vendor/
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -ldflags="-s -w" -o /out/control ./cmd/control && \
    CGO_ENABLED=0 GOOS=linux go build -mod=vendor -ldflags="-s -w" -o /out/orchestrator ./cmd/orchestrator

# ── Stage 2: runtime ────────────────────────────────────────────────────────
# node:20-bookworm-slim as base because design-phase agents invoke `claude`
# (from @anthropic-ai/claude-code) directly inside this container.
FROM node:20-bookworm-slim AS runtime

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      ca-certificates git curl gnupg && \
    # GitHub CLI official apt repo
    curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg 2>/dev/null && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] \
      https://cli.github.com/packages stable main" \
      > /etc/apt/sources.list.d/github-cli.list && \
    # Docker CLI (DooD: socket is mounted at runtime, we only need the client binary)
    curl -fsSL https://download.docker.com/linux/debian/gpg \
      | gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] \
      https://download.docker.com/linux/debian bookworm stable" \
      > /etc/apt/sources.list.d/docker.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends gh docker-ce-cli && \
    # Claude Code CLI (design-phase agents run inside this container, not sandbox)
    npm install -g @anthropic-ai/claude-code && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /out/control ./control
COPY --from=builder /out/orchestrator ./orchestrator
COPY registry/ ./registry/
COPY docker-entrypoint.sh ./docker-entrypoint.sh
RUN chmod +x ./docker-entrypoint.sh

EXPOSE 8080
ENV VIBEFORGE_REGISTRY=/app/registry
ENTRYPOINT ["./docker-entrypoint.sh"]
CMD ["./control"]
