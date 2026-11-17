// 运行平台能力：设备上做不到的功能整块不渲染，不做置灰或提示。
//
// 输入：window.wails.platform()（Wails 原生桥注入，安卓返回 "android"）
// 输出：isAndroid 常量与 platformCaps
// 预期：网页版与桌面端按全能力处理；平台只在模块加载时探测一次

interface WailsPlatformBridge {
  platform?: () => string;
}

// detectPlatform 读取平台名，取不到时返回空串。
function detectPlatform(): string {
  if (typeof window === 'undefined') return '';
  const bridge = (window as unknown as { wails?: WailsPlatformBridge }).wails;
  try {
    return typeof bridge?.platform === 'function' ? String(bridge.platform()) : '';
  } catch {
    return '';
  }
}

export const isAndroid = detectPlatform() === 'android';

// platformCaps 描述「这台设备能不能做」，不是「现在能不能做」。
//
// 输入：无（派生自 isAndroid）
// 输出：按能力名分组的布尔开关
// 预期：false 的入口在界面上完全不出现，避免点了没反应的按钮
export const platformCaps = {
  /** 下载目录页、下载按钮、打开文件夹：手机上没有对用户可见的落盘目录 */
  downloads: !isAndroid,
  /** 剪贴板粘贴上传：安卓壳里没有可读的剪贴板文件通道 */
  paste: !isAndroid,
  /** 开机自启：只有桌面端有系统注册项 */
  autostart: !isAndroid,
  /** 键盘快捷键：触摸设备没有硬件键盘 */
  shortcuts: !isAndroid,
} as const;
