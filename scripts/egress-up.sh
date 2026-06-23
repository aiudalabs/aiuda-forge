#!/usr/bin/env bash
# Bring up the egress topology for the AGENT sandbox (port of v1's F3):
#   - an INTERNAL docker network (no internet gateway): the agent has NO direct
#     egress;
#   - a dual-homed `egress-proxy` (tinyproxy, default-deny allowlist) that is the
#     ONLY way out. The agent uses HTTPS_PROXY=http://egress-proxy:8888.
#
# Default-deny: only api.anthropic.com (deploy/egress-proxy/filter) is reachable.
set -euo pipefail
HERE="$(cd "$(dirname "$0")/.." && pwd)"
NET="${VIBEFORGE_SANDBOX_NETWORK:-vibeforge-egress}"
PROXY=egress-proxy
IMG=tinyproxy-egress:local

# 1) internal network (no gateway -> no internet for members)
docker network inspect "$NET" >/dev/null 2>&1 || docker network create --internal "$NET"

# 2) build a tiny tinyproxy image (alpine + tinyproxy)
if ! docker image inspect "$IMG" >/dev/null 2>&1; then
  tmp="$(mktemp -d)"
  cp "$HERE/deploy/egress-proxy/tinyproxy.conf" "$tmp/tinyproxy.conf"
  cp "$HERE/deploy/egress-proxy/filter" "$tmp/filter"
  cat > "$tmp/Dockerfile" <<'DOCKER'
FROM alpine:3.20
RUN apk add --no-cache tinyproxy
COPY tinyproxy.conf /etc/tinyproxy/tinyproxy.conf
COPY filter /etc/tinyproxy/filter
EXPOSE 8888
ENTRYPOINT ["tinyproxy","-d","-c","/etc/tinyproxy/tinyproxy.conf"]
DOCKER
  docker build -q -t "$IMG" "$tmp" >/dev/null
  rm -rf "$tmp"
fi

# 3) run the proxy on the DEFAULT bridge (has internet), then attach it to the
#    internal network with the alias `egress-proxy` (so agents resolve it).
docker rm -f "$PROXY" >/dev/null 2>&1 || true
docker run -d --name "$PROXY" "$IMG" >/dev/null
docker network connect --alias "$PROXY" "$NET" "$PROXY"

echo "egress up: network=$NET proxy=$PROXY (allowlist: api.anthropic.com)"
