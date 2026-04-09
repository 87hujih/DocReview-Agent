import { defineConfig } from "vitest/config";

export default defineConfig({
  esbuild: {
    jsx: "automatic"
  },
  test: {
    environment: "jsdom",
    globals: true,
    include: ["components/**/*.test.tsx"],
    setupFiles: ["./vitest.setup.ts"]
  }
});
