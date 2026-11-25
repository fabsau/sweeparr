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
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: false,
    rolldownOptions: {
      output: {
        entryFileNames: "assets/[hash].js",
        chunkFileNames: "assets/[hash].js",
        assetFileNames: "assets/[hash][extname]",
        minify: {
          compress: { dropConsole: true, dropDebugger: true },
        },
        codeSplitting: {
          groups: [
            { name: "react-vendor", test: /node_modules[\/](react|react-dom|scheduler)[\/]/ },
            { name: "i18n-vendor", test: /node_modules[\/](i18next|react-i18next|i18next-browser-languagedetector)[\/]/ },
            { name: "icons", test: /node_modules[\/]lucide-react[\/]/ },
            { name: "auth-vendor", test: /node_modules[\/](@simplewebauthn[\/]browser|qrcode\.react)[\/]/ },
          ],
        },
      },
    },
  },
});
