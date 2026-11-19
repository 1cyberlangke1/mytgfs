// 视频缩略图抽帧：上传前在浏览器里截一帧，压成小 JPEG 一起发出去。
//
// 输入：用户选中的视频 File
// 输出：JPEG Blob；不适用或解码失败返回 null
// 预期：按 32KB 预算与最坏 bpp 算出缩放比例，质量 55%，只编码一次喵

const THUMB_BUDGET_BYTES = 32 << 10;
// 最坏每像素位数：质量 55% 下的高噪点画面按 2.5 bit/px 估
const THUMB_WORST_BPP = 2.5;
const THUMB_QUALITY = 0.55;
const THUMB_TIMEOUT_MS = 6000;

// thumbScale 按预算反推等比缩放比例。
//
// 输入：原图宽高
// 输出：0..1 之间的缩放比例
// 预期：噪点大时自动缩得更狠，小视频不放大
export function thumbScale(width: number, height: number): number {
  if (width <= 0 || height <= 0) return 1;
  const maxPixels = (THUMB_BUDGET_BYTES * 8) / THUMB_WORST_BPP;
  return Math.min(1, Math.sqrt(maxPixels / (width * height)));
}

// thumbPixels 是缩放后允许的最大像素数，供测试断言用。
export const THUMB_MAX_PIXELS = (THUMB_BUDGET_BYTES * 8) / THUMB_WORST_BPP;

// isVideoFile 判断这个文件值不值得抽帧。
function isVideoFile(file: File): boolean {
  return (
    file.type.startsWith('video/') ||
    /\.(mp4|m4v|mov|mkv|webm|avi|ogv|3gp)$/i.test(file.name)
  );
}

// once 等一次事件；超时就 reject，避免坏文件把上传卡死。
function once(el: HTMLVideoElement, type: 'loadeddata' | 'seeked'): Promise<void> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      el.removeEventListener(type, onDone);
      el.removeEventListener('error', onError);
      reject(new Error('timeout'));
    }, THUMB_TIMEOUT_MS);
    const onDone = () => {
      clearTimeout(timer);
      el.removeEventListener('error', onError);
      resolve();
    };
    const onError = () => {
      clearTimeout(timer);
      el.removeEventListener(type, onDone);
      reject(new Error('video error'));
    };
    el.addEventListener(type, onDone, { once: true });
    el.addEventListener('error', onError, { once: true });
  });
}

// canvasToBlob 按指定质量导出 JPEG。
function canvasToBlob(canvas: HTMLCanvasElement, quality: number): Promise<Blob | null> {
  return new Promise((resolve) => canvas.toBlob(resolve, 'image/jpeg', quality));
}

// makeVideoThumb 抽一帧并压缩。
//
// 输入：视频 File（其它类型直接返回 null）
// 输出：可直接 PUT 给服务端的 JPEG Blob
// 预期：等比缩放到长边 480 后按 55% 编码一次，结果通常在 20~30KB
export async function makeVideoThumb(file: File): Promise<Blob | null> {
  if (!isVideoFile(file)) return null;
  const url = URL.createObjectURL(file);
  const video = document.createElement('video');
  video.preload = 'auto';
  video.muted = true;
  video.playsInline = true;
  video.src = url;
  try {
    await once(video, 'loadeddata');
    const duration = video.duration;
    const seekTo = Number.isFinite(duration) && duration > 2 ? 1 : 0;
    if (seekTo > 0) {
      video.currentTime = seekTo;
      await once(video, 'seeked');
    }
    const vw = video.videoWidth;
    const vh = video.videoHeight;
    if (!vw || !vh) return null;
    // 等比缩放：宽高同乘一个系数，比例由预算反推，不写死尺寸
    const scale = thumbScale(vw, vh);
    const canvas = document.createElement('canvas');
    canvas.width = Math.max(1, Math.floor(vw * scale));
    canvas.height = Math.max(1, Math.floor(vh * scale));
    const ctx = canvas.getContext('2d');
    if (!ctx) return null;
    ctx.drawImage(video, 0, 0, canvas.width, canvas.height);
    return await canvasToBlob(canvas, THUMB_QUALITY);
  } catch {
    return null;
  } finally {
    URL.revokeObjectURL(url);
  }
}
