#!/bin/bash
# Arranque del control-plane de aiuda-forge.
# Fuente de secretos: ~/.config/aiuda-forge/secrets.env
set -a
source ~/.config/aiuda-forge/secrets.env 2>/dev/null
set +a

cd "$(dirname "$0")"

exec ./control \
  VIBEFORGE_ENGINE=claude \
  VIBEFORGE_CORS=open \
  VIBEFORGE_ALLOW_LOCAL_SANDBOX=1 \
  "$@"
