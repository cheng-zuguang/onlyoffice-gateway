import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(__dirname, './src') } },
  server: {
    port: 16666,
    proxy: {
      '/admin/api': {
        target: 'http://localhost:18080',
        changeOrigin: true,
      },
    },
  },
})
