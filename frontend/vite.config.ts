import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 3000,
    proxy: {
      "/api": {
        target: process.env.ARGUS_API_PROXY || "http://localhost:8080",
        ws: true,
      },
    },
  },
  build: {
    chunkSizeWarningLimit: 1024,
  },
});
