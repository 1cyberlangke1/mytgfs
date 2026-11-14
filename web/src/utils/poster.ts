// 从视频文件抽一帧做封面。
//
// 输入：file 视频文件；maxSide 最长边像素
// 输出：JPEG Blob；浏览器无法解码视频时返回 null
// 预期：不抛错、不阻塞上传主流程，失败由调用方忽略

export async function makeVideoPoster(file: File, maxSide = 320): Promise<Blob | null> {
  const url = URL.createObjectURL(file);
  const video = document.createElement('video');
  video.src = url;
  video.muted = true;
  video.playsInline = true;
  video.preload = 'auto';
  try {
    await new Promise<void>((resolve, reject) => {
      const timer = window.setTimeout(() => reject(new Error('poster timeout')), 5000);
      video.onloadedmetadata = () => {
        window.clearTimeout(timer);
        resolve();
      };
      video.onerror = () => {
        window.clearTimeout(timer);
        reject(new Error('poster load failed'));
      };
    });
    const duration = Number.isFinite(video.duration) ? video.duration : 0;
    const at = duration > 0 ? Math.min(1, duration * 0.25) : 0;
    await new Promise<void>((resolve) => {
      const timer = window.setTimeout(() => resolve(), 3000);
      video.onseeked = () => {
        window.clearTimeout(timer);
        resolve();
      };
      try {
        video.currentTime = at;
      } catch {
        window.clearTimeout(timer);
        resolve();
      }
    });
    const w = video.videoWidth || maxSide;
    const h = video.videoHeight || maxSide;
    const scale = Math.min(1, maxSide / Math.max(w, h));
    const canvas = document.createElement('canvas');
    canvas.width = Math.max(1, Math.round(w * scale));
    canvas.height = Math.max(1, Math.round(h * scale));
    const ctx = canvas.getContext('2d');
    if (!ctx) return null;
    ctx.drawImage(video, 0, 0, canvas.width, canvas.height);
    return await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/jpeg', 0.82));
  } catch {
    return null;
  } finally {
    URL.revokeObjectURL(url);
    video.removeAttribute('src');
    video.load();
  }
}
