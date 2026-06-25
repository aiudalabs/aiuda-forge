#!/usr/bin/env bash
# Tear down the egress topology.
set -euo pipefail
NET="${VIBEFORGE_SANDBOX_NETWORK:-vibeforge-egress}"
docker rm -f egress-proxy >/dev/null 2>&1 || true
docker network rm "$NET" >/dev/null 2>&1 || true
echo "egress down: removed proxy + network $NET"
