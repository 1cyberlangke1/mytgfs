import { createRoot } from 'react-dom/client';
import App from './App.tsx';
import './index.css';

createRoot(document.getElementById('root')!).render(<App />);

// 桌面壳（Wails）内不安装 Service Worker：先清掉历史注册与缓存，
// 否则旧 SW 会继续接管页面并返回缓存的旧 index.html。
// 网页版（`mytgfs web`）才注册，用于可安装与离线阅读。
const inShell = typeof window !== 'undefined' && '_wails' in window;

if ('serviceWorker' in navigator) {
  if (inShell) {
    navigator.serviceWorker
      .getRegistrations()
      .then((regs) => Promise.all(regs.map((reg) => reg.unregister())))
      .catch(() => {});
    if ('caches' in window) {
      caches
        .keys()
        .then((keys) => Promise.all(keys.map((key) => caches.delete(key))))
        .catch(() => {});
    }
  } else {
    window.addEventListener('load', () => {
      navigator.serviceWorker.register('/sw.js').catch(() => {});
    });
  }
}
