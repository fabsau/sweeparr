import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": "http://localhost:8080",
      "/healthz": "http://localhost:8080",
    },
  },
  esbuild: {
    drop: ["console", "debugger"],
    legalComments: "none",
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: false,
    minify: "esbuild",
    rollupOptions: {
      output: {
        entryFileNames: "assets/[hash].js",
        chunkFileNames: "assets/[hash].js",
        assetFileNames: "assets/[hash][extname]",
        manualChunks: {
          "react-vendor": ["react", "react-dom"],
          "i18n-vendor": ["i18next", "react-i18next", "i18next-browser-languagedetector"],
          "icons": ["lucide-react"],
          "auth-vendor": ["@simplewebauthn/browser", "qrcode.react"],
        },
      },
    },
  },
});
