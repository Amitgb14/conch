import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

// conch-ui: the views conch's desktop apps draw with HTML — the Mac app's
// web views now, the Linux and Windows app's renderer later. One script
// and one stylesheet, named without hashes so the build is reproducible:
// scripts/swift.mjs writes them into ConchKit, where Conch.app takes them
// with `swift build` alone, and CI rebuilds them to check nothing moved.
export default defineConfig({
  plugins: [react()],
  define: { "process.env.NODE_ENV": JSON.stringify("production") },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    minify: true,
    cssCodeSplit: false,
    lib: {
      entry: "src/main.tsx",
      formats: ["iife"],
      name: "conchUIBundle",
      fileName: () => "conch-ui.js",
      cssFileName: "conch-ui",
    },
  },
})
