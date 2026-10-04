// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// KRYTON_DEV_API points the dev proxy at a running krytond (local or lab host).
const api = process.env.KRYTON_DEV_API || 'http://127.0.0.1:8080';
const upstream = { target: api, secure: false, changeOrigin: true };

// The build lands in cmd/krytond/web, which krytond embeds with //go:embed.
// The output is committed so Go builds never need Node.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../cmd/krytond/web',
    emptyOutDir: true,
    sourcemap: false,
    chunkSizeWarningLimit: 800,
  },
  server: {
    port: 5174,
    proxy: {
      '/api': { ...upstream, ws: true },
      '/readyz': upstream,
      '/healthz': upstream,
    },
  },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
});
