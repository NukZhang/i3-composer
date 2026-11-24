import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';

// 构建产物必须落在 web/dist：BFF 用 spa(http.Dir("web/dist")) 托管。
// 开发时用 vite 的 /api 代理连本地 BFF，生产构建后同源，不需要任何 CORS 配置。
export default defineConfig({
  plugins: [vue()],
  build: { outDir: 'dist', emptyOutDir: true },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://127.0.0.1:8080', changeOrigin: true, ws: false },
    },
  },
});
