import createMDX from "@next/mdx"
import type { NextConfig } from "next"

const nextConfig: NextConfig = {
  // Static HTML in out/, so the site can be served from any static host.
  output: "export",
  trailingSlash: true,
  // Set BASE_PATH=/conch when serving from a subpath such as GitHub Pages.
  basePath: process.env.BASE_PATH || "",
  pageExtensions: ["ts", "tsx", "md", "mdx"],
  images: { unoptimized: true },
}

const withMDX = createMDX({
  options: {
    // Plugin names as strings so Turbopack can load them.
    remarkPlugins: ["remark-gfm"],
    rehypePlugins: ["rehype-slug"],
  },
})

export default withMDX(nextConfig)
