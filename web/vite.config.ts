import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";
export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: "/admin/",
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  build: { outDir: "../internal/api/webdist", emptyOutDir: true },
  server: {
    port: 5173,
    proxy: {
      "/v3": {
        target: process.env.MEDIAMTX_API_URL || "http://127.0.0.1:9997",
        changeOrigin: true,
      },
    },
  },
});
