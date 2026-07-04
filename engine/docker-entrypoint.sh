#!/bin/sh
set -e

# Configure git credentials using GH_TOKEN so the control plane can
# clone private GitHub repos without interactive prompts.
if [ -n "$GH_TOKEN" ]; then
    git config --global credential.helper store
    printf 'https://oauth2:%s@github.com\n' "$GH_TOKEN" > /root/.git-credentials
    git config --global url."https://oauth2:${GH_TOKEN}@github.com/".insteadOf "https://github.com/"
fi

exec "$@"
