import type { NextConfig } from "next";

// In production the gateway routes /api, /sendgrid and /ses straight to the Go
// backend. For local development (next dev / docker-compose) we proxy them.
// NOTE: evaluated at build time (also for `output: standalone`), so set
// API_UPSTREAM when building, not only when running.
const upstream = process.env.API_UPSTREAM || "http://localhost:8080";

// NEXT_OUTPUT=export builds a static site (lite edition: the Go binary embeds and
// serves it, so no rewrites/redirects/headers apply; the server handles those).
const isExport = process.env.NEXT_OUTPUT === "export";

const nextConfig: NextConfig = isExport ? {
  output: "export",
  reactStrictMode: true,
  poweredByHeader: false,
  images: { unoptimized: true },
} : {
  output: "standalone",
  reactStrictMode: true,
  poweredByHeader: false,
  async rewrites() {
    return [
      { source: "/api/:path*", destination: `${upstream}/api/:path*` },
      { source: "/sendgrid/:path*", destination: `${upstream}/sendgrid/:path*` },
      { source: "/ses/:path*", destination: `${upstream}/ses/:path*` },
      { source: "/ses", destination: `${upstream}/ses` },
      { source: "/azure/:path*", destination: `${upstream}/azure/:path*` },
      { source: "/graph/:path*", destination: `${upstream}/graph/:path*` },
      { source: "/gmail/:path*", destination: `${upstream}/gmail/:path*` },
    ];
  },
  async redirects() {
    return [
      { source: "/guide", destination: "/guide/index.html", permanent: false },
      { source: "/guide/", destination: "/guide/index.html", permanent: false },
    ];
  },
  async headers() {
    return [
      {
        source: "/:path*",
        headers: [
          { key: "X-Frame-Options", value: "SAMEORIGIN" },
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "Referrer-Policy", value: "same-origin" },
        ],
      },
    ];
  },
};

export default nextConfig;
