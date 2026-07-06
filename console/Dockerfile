# ── Stage 1: deps ──────────────────────────────────────────────────────────
FROM node:20-alpine AS deps
WORKDIR /app
COPY package*.json ./
RUN npm ci

# ── Stage 2: build ─────────────────────────────────────────────────────────
FROM node:20-alpine AS builder
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
ENV NEXT_TELEMETRY_DISABLED=1
# FORGE_API_INTERNAL is used by next.config.ts rewrites at build time (SSR proxy).
ARG FORGE_API_INTERNAL=http://control:8080
ENV FORGE_API_INTERNAL=${FORGE_API_INTERNAL}
# NEXT_PUBLIC_VIBEFORGE_API_URL is BAKED here (Next inlines NEXT_PUBLIC_* at build) — it's
# the URL the BROWSER uses to reach the control plane. For a real deploy behind one domain
# + reverse proxy, set it to https://<domain>/forge-api (see deploy/docker-compose.prod.yml).
# Defaults to localhost:8080 for local dev.
ARG NEXT_PUBLIC_VIBEFORGE_API_URL=http://localhost:8080
ENV NEXT_PUBLIC_VIBEFORGE_API_URL=${NEXT_PUBLIC_VIBEFORGE_API_URL}
RUN npm run build

# ── Stage 3: runtime ───────────────────────────────────────────────────────
FROM node:20-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1

RUN addgroup --system --gid 1001 nodejs && \
    adduser --system --uid 1001 nextjs

COPY --from=builder /app/public ./public
COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static

USER nextjs
EXPOSE 3000
ENV PORT=3000
CMD ["node", "server.js"]
