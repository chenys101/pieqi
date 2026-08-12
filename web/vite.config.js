import { defineConfig } from 'vite';

export default defineConfig({
  root: '.',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    host: true,        // 监听所有网络接口（支持 cloudflare 隧道等公网访问）
    allowedHosts: true, // 允许所有 host（开发用；生产应配具体域名）
    port: 5174,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:3000',
        changeOrigin: true,
        ws: true,
      },
      '/internal': 'http://127.0.0.1:3000',
    },
  },
});
