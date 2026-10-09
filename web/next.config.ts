import type { NextConfig } from "next";

const controlPlaneUrl = process.env.CONTROL_PLANE_URL ?? "http://localhost:8000";

const nextConfig: NextConfig = {
  async rewrites() {
    return [{ source: "/api/v1/:path*", destination: `${controlPlaneUrl}/api/v1/:path*` }];
  },
};

export default nextConfig;
