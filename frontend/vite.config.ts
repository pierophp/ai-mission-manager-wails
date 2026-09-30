import path from "node:path";
import { defineConfig, lazyPlugins } from "vite-plus";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import wails from "@wailsio/runtime/plugins/vite";

export default defineConfig({
  fmt: {
    ignorePatterns: [
      ".agents/**",
      "docs/**",
      "idea.md",
      "package.json",
      "README.md",
      "spikes/**",
    ],
    printWidth: 80,
  },
  lint: {
    ignorePatterns: [
      ".agents/**",
      ".claude/**",
      "docs/**",
      "idea.md",
      "package.json",
      "README.md",
      "spikes/**",
    ],
    jsPlugins: [{ name: "vite-plus", specifier: "vite-plus/oxlint-plugin" }],
    rules: { "vite-plus/prefer-vite-plus-imports": "error" },
    options: { typeAware: true, typeCheck: true },
  },
  check: { fmt: false },
  test: {
    include: ["src/**/*.{test,spec}.{js,ts,jsx,tsx}"],
    environment: "happy-dom",
    passWithNoTests: true,
  },
  plugins: lazyPlugins(() => [react(), tailwindcss(), wails("./bindings")]),
  resolve: {
    alias: {
      "@": path.resolve(process.cwd(), "./src"),
      "@tauri-apps/api/core": path.resolve(process.cwd(), "./src/wails/core.ts"),
      "@tauri-apps/api/event": path.resolve(process.cwd(), "./src/wails/event.ts"),
      "@tauri-apps/plugin-opener": path.resolve(process.cwd(), "./src/wails/opener.ts"),
      "@tauri-apps/plugin-dialog": path.resolve(process.cwd(), "./src/wails/dialog.ts"),
    },
  },
  clearScreen: false,
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
});
