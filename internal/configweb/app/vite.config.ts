import path from "node:path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

// The build lands in ../dist, which the Go package embeds: conch is built
// with `go build` alone, so the output is committed and `make settings-ui`
// remakes it after a change here.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: "./",
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "./src") } },
  build: { outDir: "../dist", emptyOutDir: true, assetsDir: "assets", chunkSizeWarningLimit: 1000 },
  server: { proxy: { "/api": process.env.CONCH_SETTINGS_URL ?? "http://127.0.0.1:8723" } },
})
