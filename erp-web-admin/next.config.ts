import type { NextConfig } from "next";

// Development only: let the dev server answer pages opened from other
// devices on the LAN (a tablet or phone at http://10.x.x.x:3000) — Next.js
// blocks cross-origin requests to its dev resources (HMR, fonts) unless the
// host is listed. Private-network ranges by default; add more with
// NEXT_DEV_ALLOWED_ORIGINS (comma-separated hosts or wildcards). Has no
// effect on a production build.
const allowedDevOrigins = [
  "10.*.*.*",
  "192.168.*.*",
  ...Array.from({ length: 16 }, (_, i) => `172.${16 + i}.*.*`),
  ...(process.env.NEXT_DEV_ALLOWED_ORIGINS ?? "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean),
];

const nextConfig: NextConfig = {
  allowedDevOrigins,
};

export default nextConfig;
