import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

export default defineConfig(() => {
  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: {
        '@': import.meta.dirname,
      },
    },
    build: {
      rolldownOptions: {
        output: {
          // 第三方依赖分块：业务主包只留自己的代码，避免单文件超过 500 kB。
          // 输入：模块 id；输出：分块组；预期：react/i18n/motion 各自成 chunk，其余进 vendor。
          codeSplitting: {
            groups: [
              { name: 'react', test: /node_modules[\\/](react|react-dom|scheduler)[\\/]/ },
              { name: 'i18n', test: /node_modules[\\/](i18next|react-i18next)[\\/]/ },
              { name: 'motion', test: /node_modules[\\/](motion|framer-motion|motion-dom|motion-utils)[\\/]/ },
              { name: 'vendor', test: /node_modules[\\/]/ },
            ],
          },
        },
      },
    },
    server: {
      // HMR is disabled in AI Studio via DISABLE_HMR env var.
      hmr: process.env.DISABLE_HMR !== 'true',
      watch: process.env.DISABLE_HMR === 'true' ? null : {},
    },
    test: {
      // 纯函数单测跑在 node 环境；无需 DOM
      environment: 'node',
    },
  };
});
