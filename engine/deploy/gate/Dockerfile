# Gate sandbox image: runs a repo's .vibeforge-gate test command in a no-network
# container. Carries BOTH toolchains so python (pytest/unittest) and node
# (npm test / vitest) projects gate from the same image, without per-lane images
# (that isolation is Phase B2). node_modules / a workdir venv persist from the
# implement step into this offline gate via the shared workdir mount.
FROM node:20-bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      python3 python3-venv python3-pip ca-certificates \
    && rm -rf /var/lib/apt/lists/*
