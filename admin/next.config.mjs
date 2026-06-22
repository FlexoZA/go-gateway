/** @type {import('next').NextConfig} */
const nextConfig = {
  // Build a self-contained server bundle for a small Docker image.
  output: "standalone",
  reactStrictMode: true,
  // The admin panel ships without an ESLint config; never block a build on it.
  eslint: { ignoreDuringBuilds: true },
};

export default nextConfig;
