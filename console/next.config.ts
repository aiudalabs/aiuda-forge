import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  // Standalone output for efficient Docker images (no node_modules copy in prod).
  output: "standalone",
  // Same-origin proxy to the control plane: when API_URL is set to "/forge-api"
  // (e.g. behind a single public tunnel), API calls hit the Next server and are
  // rewritten to the local control plane — no CORS, one URL to share. Unused in
  // normal dev where API_URL points straight at http://localhost:8080.
  // FORGE_API_INTERNAL lets the Docker container proxy to the `control` service
  // by name instead of localhost.
  async rewrites() {
    const api = process.env.FORGE_API_INTERNAL ?? "http://localhost:8080";
    return [{ source: "/forge-api/:path*", destination: `${api}/:path*` }];
  },
};

export default nextConfig;
