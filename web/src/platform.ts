// 运行平台能力：设备上做不到的功能整块不渲染，不做置灰或提示。
//
// 输入：window.mytgfsNative.platform()（安卓宿主注入的 Java 桥，返回 "android"）
// 输出：isAndroid 常量与 platformCaps
// 预期：网页版与桌面端按全能力处理；平台只在模块加载时探测一次；
//       Wails 运行时加载后会把 window.wails 覆盖成自己的对象，所以桥必须走
//       专属名 mytgfsNative，否则调用期拿不到 Java 方法

interface WailsPlatformBridge {
  platform?: () => string;
}

// nativeBridge 取安卓宿主注入的 Java 桥对象。
//
// 输入：无
// 输出：桥对象；纯浏览器里为 undefined
// 预期：只认 mytgfsNative，不回退 window.wails（那个名字归 Wails 运行时）
function nativeBridge<T>(): T | undefined {
  if (typeof window === 'undefined') return undefined;
  return (window as unknown as { mytgfsNative?: T }).mytgfsNative;
}

// detectPlatform 读取平台名，取不到时返回空串。
function detectPlatform(): string {
  const bridge = nativeBridge<WailsPlatformBridge>();
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
  /** 下载按钮与下载页：安卓也能下载进应用沙盒，再交给系统播放器打开 */
  downloads: true,
  /** 下载页那个目录按钮按什么行为渲染：桌面用文件管理器打开，安卓导出到公共下载目录 */
  downloadsDirAction: isAndroid ? 'export' : 'open',
  /** 剪贴板粘贴上传：安卓壳里没有可读的剪贴板文件通道 */
  paste: !isAndroid,
  /** 开机自启：只有桌面端有系统注册项 */
  autostart: !isAndroid,
  /** 键盘快捷键：触摸设备没有硬件键盘 */
  shortcuts: !isAndroid,
  /**
   * 上传/头像走「原生系统选择器 + fd 直读」。
   *
   * 安卓的 JS↔Java 桥只能传字符串，走文件字节就得 base64；桌面端用的是
   * 浏览器 File 对象 + fetch（二进制直传，没有编码问题），所以只有安卓开启。
   * 安卓这条链不复制到应用 cache：选择器给的 SAF 文件打开成 fd，直接交给服务端。
   */
  pickPaths: isAndroid,
  /** 频道头像用原生图片选择器（fd 直读，不复制到 cache） */
  coverPicker: isAndroid,
} as const;

// WailsJsBridge 是安卓宿主注入的 JavascriptInterface（window.mytgfsNative）。
interface WailsJsBridge {
  openFile?: (path: string) => void;
  exportToDownloads?: (path: string) => boolean;
}

// openNativeFile 让系统用默认应用打开沙盒里的文件。
//
// 输入：绝对路径（服务端返回的下载落盘路径）
// 输出：无；桥不可用或调用抛错时同步抛异常
// 预期：安卓走 FileProvider + ACTION_VIEW，桌面端不该调用本函数
export function openNativeFile(path: string): void {
  const bridge = nativeBridge<WailsJsBridge>();
  if (typeof bridge?.openFile !== 'function') {
    throw new Error('native open unavailable');
  }
  bridge.openFile(path);
}

// exportNativeFile 把沙盒里的文件复制到系统公共下载目录（MediaStore）。
//
// 输入：绝对路径（服务端返回的下载落盘路径）
// 输出：true 表示已落进公共 Downloads，系统的文件管理器能看到
// 预期：安卓走 MediaStore 的 IS_PENDING 两步写入；桌面端不该调用本函数
export function exportNativeFile(path: string): boolean {
  const bridge = nativeBridge<WailsJsBridge>();
  if (typeof bridge?.exportToDownloads !== 'function') {
    throw new Error('native export unavailable');
  }
  return bridge.exportToDownloads(path) === true;
}
