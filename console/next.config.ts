import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  // Same-origin proxy to the control plane: when API_URL is set to "/forge-api"
  // (e.g. behind a single public tunnel), API calls hit the Next server and are
  // rewritten to the local control plane — no CORS, one URL to share. Unused in
  // normal dev where API_URL points straight at http://localhost:8080.
  async rewrites() {
    return [{ source: "/forge-api/:path*", destination: "http://localhost:8080/:path*" }];
  },
};

export default nextConfig;
