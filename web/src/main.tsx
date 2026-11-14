import { createRoot } from 'react-dom/client';
import App from './App.tsx';
// 字体必须随包内嵌：桌面壳与离线环境没有 Google Fonts，在线引用会静默回落到系统字体
import '@fontsource/plus-jakarta-sans/latin-400.css';
import '@fontsource/plus-jakarta-sans/latin-500.css';
import '@fontsource/plus-jakarta-sans/latin-600.css';
import '@fontsource/plus-jakarta-sans/latin-700.css';
import '@fontsource/bricolage-grotesque/latin-500.css';
import '@fontsource/bricolage-grotesque/latin-600.css';
import '@fontsource/bricolage-grotesque/latin-700.css';
import '@fontsource/jetbrains-mono/latin-400.css';
import '@fontsource/jetbrains-mono/latin-500.css';
import '@fontsource/jetbrains-mono/latin-600.css';
import './index.css';
import { initPushRuntime } from './events';

// 渲染前先接好推送运行时：桌面壳的运行时对象只有 bootstrap 完才存在，
// 面板要早点订阅才能不漏掉「卷已挂载」这类事件。
//
// 输入：无
// 输出：无（渲染 App）
// 预期：网页版 initPushRuntime 直接返回，App 用 SSE
async function boot() {
  await initPushRuntime();
  createRoot(document.getElementById('root')!).render(<App />);
}

void boot();

// 历史版本注册过 Service Worker（离线 PWA 已移除）：
// 不清掉会继续接管页面并返回缓存的旧 index.html。
if ('serviceWorker' in navigator) {
  navigator.serviceWorker
    .getRegistrations()
    .then((regs) => Promise.all(regs.map((reg) => reg.unregister())))
    .catch(() => {});
}
if ('caches' in window) {
  caches
    .keys()
    .then((keys) => Promise.all(keys.map((key) => caches.delete(key))))
    .catch(() => {});
}
