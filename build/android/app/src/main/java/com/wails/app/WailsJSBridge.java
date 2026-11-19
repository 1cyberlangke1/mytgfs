package com.wails.app;

import android.util.Log;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import android.webkit.JavascriptInterface;
import android.webkit.WebView;
import com.wails.app.BuildConfig;
import android.graphics.Bitmap;
import android.media.MediaMetadataRetriever;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileOutputStream;

/**
 * WailsJSBridge provides the JavaScript interface that allows the web frontend
 * to communicate with the Go backend. This is exposed to JavaScript as the
 * `window.wails` object.
 *
 * Similar to iOS's WKScriptMessageHandler but using Android's addJavascriptInterface.
 */
public class WailsJSBridge {
    private static final String TAG = "WailsJSBridge";
    private static final boolean DEBUG = BuildConfig.DEBUG;
    // 缩略图预算与最坏每像素位数：缩放比例由这两个数反推，不写死尺寸
    private static final int THUMB_BUDGET_BYTES = 32 * 1024;
    private static final double THUMB_WORST_BPP = 2.5;
    private static final int THUMB_QUALITY = 55;
    // Pooled threads avoid unbounded thread creation under high call volume.
    private static final ExecutorService executor = Executors.newCachedThreadPool();

    private final WailsBridge bridge;
    private final WebView webView;

    public WailsJSBridge(WailsBridge bridge, WebView webView) {
        this.bridge = bridge;
        this.webView = webView;
    }

    /**
     * Send a message to Go and return the response synchronously.
     * Called from JavaScript: wails.invoke(message)
     *
     * @param message The message to send (JSON string)
     * @return The response from Go (JSON string)
     */
    @JavascriptInterface
    public String invoke(String message) {
        if (DEBUG) Log.d(TAG, "Invoke called: " + message);
        return bridge.handleMessage(message);
    }

    /**
     * Send a message to Go asynchronously.
     * The response will be sent back via a callback.
     * Called from JavaScript: wails.invokeAsync(callbackId, message)
     *
     * @param callbackId The callback ID to use for the response
     * @param message The message to send (JSON string)
     */
    @JavascriptInterface
    public void invokeAsync(final String callbackId, final String payload) {
        if (DEBUG) Log.d(TAG, "InvokeAsync called: " + payload);

        // Handle off the JS thread so we don't block the WebView.
        executor.execute(() -> {
            try {
                String response = bridge.handleRuntimeCall(payload);
                sendCallback(callbackId, response, null);
            } catch (Exception e) {
                Log.e(TAG, "Error in async invoke", e);
                sendCallback(callbackId, null, e.getMessage());
            }
        });
    }

    /**
     * Log a message from JavaScript to Android's logcat
     * Called from JavaScript: wails.log(level, message)
     *
     * @param level The log level (debug, info, warn, error)
     * @param message The message to log
     */
    @JavascriptInterface
    public void log(String level, String message) {
        switch (level.toLowerCase()) {
            case "debug":
                Log.d(TAG + "/JS", message);
                break;
            case "info":
                Log.i(TAG + "/JS", message);
                break;
            case "warn":
                Log.w(TAG + "/JS", message);
                break;
            case "error":
                Log.e(TAG + "/JS", message);
                break;
            default:
                Log.v(TAG + "/JS", message);
                break;
        }
    }

    /**
     * Get the platform name
     * Called from JavaScript: wails.platform()
     *
     * @return "android"
     */
    @JavascriptInterface
    public String platform() {
        return "android";
    }

    /**
     * Check if we're running in debug mode
     * Called from JavaScript: wails.isDebug()
     *
     * @return true if debug build, false otherwise
     */
    @JavascriptInterface
    public boolean isDebug() {
        return BuildConfig.DEBUG;
    }

    /**
     * Open a sandbox file with the system "open with" chooser.
     * Called from JavaScript: wails.openFile(path)
     *
     * @param path absolute path under the app's files or cache directory
     */
    @JavascriptInterface
    public void openFile(String path) {
        bridge.openFile(path);
    }

    /**
     * Copy a sandbox file into the public Downloads collection.
     * Called from JavaScript: wails.exportToDownloads(path)
     *
     * @param path absolute path under the app's files or cache directory
     * @return true when the copy landed in MediaStore.Downloads
     */
    @JavascriptInterface
    public boolean exportToDownloads(String path) {
        return bridge.exportToDownloads(path);
    }

    /**
     * Relaunch the app so it re-reads config.toml after first-time setup.
     * Called from JavaScript: wails.restartApp()
     */
    @JavascriptInterface
    public void restartApp() {
        bridge.restartApp();
    }

    /**
     * Send a callback response to JavaScript
     */
    private void sendCallback(String callbackId, String result, String error) {
        final String js;
        if (error != null) {
            js = String.format(
                    "window._wailsAndroidCallback && window._wailsAndroidCallback('%s', null, '%s');",
                    escapeJsString(callbackId),
                    escapeJsString(error)
            );
        } else {
            js = String.format(
                    "window._wailsAndroidCallback && window._wailsAndroidCallback('%s', '%s', null);",
                    escapeJsString(callbackId),
                    escapeJsString(result != null ? result : "")
            );
        }

        webView.post(() -> webView.evaluateJavascript(js, null));
    }

    private String escapeJsString(String str) {
        if (str == null) return "";
        return str.replace("\\", "\\\\")
                .replace("'", "\\'")
                .replace("\n", "\\n")
                .replace("\r", "\\r")
                // JS line terminators (U+2028/U+2029) must be escaped too; built via
                // (char) casts so the Java lexer does not reinterpret them as newlines.
                .replace(String.valueOf((char) 0x2028), "\\u2028")
                .replace(String.valueOf((char) 0x2029), "\\u2029");
    }

    // ---- 单文件导入（系统选择器 → fd 直读，字节不进 JS）----
    //
    // 不用 Wails 的 Dialogs.OpenFile：它在安卓把 SAF 文件复制进 cache 再返回路径，
    // 副本没有任何回收路径，用久了 cache 里全是垃圾。这里直接把 fd 号交给 Go。

    public static final int FILE_IMPORT_REQUEST = 7003;

    private String fileVol = "";
    private String fileDir = "/";
    private boolean fileBusy = false;
    private int fileOk = 0;
    private int fileFailed = 0;

    /** 打开系统文件选择器（可多选）；完成后回调 window._mytgfsFileImport(ok, failed)。 */
    @JavascriptInterface
    public void pickFileImport(String vol, String dir) {
        if (fileBusy) return;
        fileBusy = true;
        fileOk = 0;
        fileFailed = 0;
        fileVol = vol == null ? "" : vol;
        fileDir = (dir == null || dir.isEmpty()) ? "/" : dir;
        try {
            android.app.Activity act = (android.app.Activity) webView.getContext();
            android.content.Intent intent = new android.content.Intent(
                    android.content.Intent.ACTION_OPEN_DOCUMENT);
            intent.addCategory(android.content.Intent.CATEGORY_OPENABLE);
            intent.setType("*/*");
            intent.putExtra(android.content.Intent.EXTRA_ALLOW_MULTIPLE, true);
            intent.addFlags(android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION);
            act.startActivityForResult(intent, FILE_IMPORT_REQUEST);
        } catch (Exception e) {
            Log.e(TAG, "pickFileImport failed", e);
            finishFileImport();
        }
    }

    /** 由 MainActivity 在 onActivityResult 转发（多选来自 ClipData）。 */
    public void onFilesPicked(final android.content.Intent data) {
        executor.execute(() -> {
            java.util.List<android.net.Uri> uris = new java.util.ArrayList<>();
            if (data != null) {
                android.content.ClipData clip = data.getClipData();
                if (clip != null) {
                    for (int i = 0; i < clip.getItemCount(); i++) {
                        android.net.Uri u = clip.getItemAt(i).getUri();
                        if (u != null) uris.add(u);
                    }
                } else if (data.getData() != null) {
                    uris.add(data.getData());
                }
            }
            for (android.net.Uri uri : uris) {
                String name = displayName(uri);
                if (adoptOne(fileVol, fileDir, uri, name)) fileOk++;
                else fileFailed++;
            }
            finishFileImport();
        });
    }

    private String displayName(android.net.Uri uri) {
        String name = null;
        try (android.database.Cursor c = webView.getContext().getContentResolver().query(
                uri, new String[]{android.provider.OpenableColumns.DISPLAY_NAME},
                null, null, null)) {
            if (c != null && c.moveToFirst()) name = c.getString(0);
        } catch (Exception e) {
            Log.w(TAG, "displayName failed", e);
        }
        if (name == null || name.isEmpty()) {
            String tail = uri.getLastPathSegment();
            name = (tail == null || tail.isEmpty()) ? "file" : tail;
        }
        return name;
    }

    private void finishFileImport() {
        fileBusy = false;
        final String js = "window._mytgfsFileImport && window._mytgfsFileImport("
                + fileOk + "," + fileFailed + ");";
        webView.post(() -> webView.evaluateJavascript(js, null));
    }

    // ---- 文件夹导入（系统选择器 → fd 直读，字节不进 JS）----
    //
    // 流程：选目录 → 递归枚举 SAF 节点 → 每个文件打开 fd →
    // 用 /proc/self/fd/N 作为路径交给 Go 接管 → 关闭 fd。
    // Java 只做系统调用与协议转发；放哪个卷、怎么命名都由 Go 决定。

    public static final int FOLDER_IMPORT_REQUEST = 7004;

    private String folderVol = "";
    private String folderDir = "/";
    private boolean folderBusy = false;
    /** 失败项的相对路径，按出现顺序记录，结束时整份回传前端。 */
    private final java.util.List<String> folderFailedNames = new java.util.ArrayList<>();

    /** 打开系统目录选择器；完成后通过 window._mytgfsFolderImport(ok, failed) 回调。 */
    @JavascriptInterface
    public void pickFolderImport(String vol, String dir) {
        if (folderBusy) return;
        folderBusy = true;
        folderFailedNames.clear();
        folderVol = vol == null ? "" : vol;
        folderDir = (dir == null || dir.isEmpty()) ? "/" : dir;
        try {
            android.app.Activity act = (android.app.Activity) webView.getContext();
            android.content.Intent intent =
                    new android.content.Intent(android.content.Intent.ACTION_OPEN_DOCUMENT_TREE);
            intent.addFlags(android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION);
            act.startActivityForResult(intent, FOLDER_IMPORT_REQUEST);
        } catch (Exception e) {
            Log.e(TAG, "pickFolderImport failed", e);
            finishFolderImport(0);
        }
    }

    /** 由 MainActivity 在 onActivityResult 转发。 */
    public void onFolderPicked(final android.net.Uri treeUri) {
        if (treeUri == null) {
            Log.i(TAG, "folder import cancelled");
            finishFolderImport(0);
            return;
        }
        Log.i(TAG, "folder import start: " + treeUri);
        executor.execute(() -> {
            int ok;
            try {
                ok = importChildren(treeUri, childrenOf(treeUri, android.provider.DocumentsContract
                        .getTreeDocumentId(treeUri)), "");
            } catch (Exception e) {
                Log.e(TAG, "folder import failed", e);
                ok = 0;
            }
            finishFolderImport(ok);
        });
    }

    private android.net.Uri childrenOf(android.net.Uri treeUri, String docId) {
        return android.provider.DocumentsContract.buildChildDocumentsUriUsingTree(treeUri, docId);
    }

    private int importChildren(android.net.Uri tree, android.net.Uri childrenUri, String relDir) {
        int ok = 0;
        String[] cols = {
                android.provider.DocumentsContract.Document.COLUMN_DOCUMENT_ID,
                android.provider.DocumentsContract.Document.COLUMN_DISPLAY_NAME,
                android.provider.DocumentsContract.Document.COLUMN_MIME_TYPE,
        };
        try (android.database.Cursor c = webView.getContext().getContentResolver()
                .query(childrenUri, cols, null, null, null)) {
            while (c != null && c.moveToNext()) {
                String id = c.getString(0);
                String name = c.getString(1);
                String mime = c.getString(2);
                if (name == null || name.isEmpty()) name = "file";
                String rel = relDir.isEmpty() ? name : relDir + "/" + name;
                if (android.provider.DocumentsContract.Document.MIME_TYPE_DIR.equals(mime)) {
                    // 目录必须先建出来：空目录也要在远端存在，不能等第一个文件带出来
                    if (!ensureDir(folderVol, relDir, name)) folderFailedNames.add(rel + "/");
                    ok += importChildren(tree, childrenOf(tree, id), rel);
                    continue;
                }
                android.net.Uri doc = android.provider.DocumentsContract.buildDocumentUriUsingTree(tree, id);
                if (adoptOne(folderVol, folderDir, doc, rel)) ok++;
                else folderFailedNames.add(rel);
            }
        } catch (Exception e) {
            Log.e(TAG, "enumerate failed", e);
            folderFailedNames.add(relDir.isEmpty() ? "/" : relDir + "/");
        }
        return ok;
    }

    /**
     * postViaRuntime 用 Wails 运行时通道发一次 POST。
     *
     * 输入：path 带 query；body 为 JSON 字符串（无请求体传 ""）
     * 输出：true 表示 Go 侧返回 {"ok":true,...}
     * 预期：asset 通道（serveAsset）只适用于 GET，POST 必须走 Bridge.Do RPC，
     *       否则 Go 只能看到 GET，处理函数直接拒绝、正文为空
     */
    private boolean postViaRuntime(String path, String body) {
        String payload = "{\"object\":0,\"method\":0,\"windowName\":\"\",\"args\":{\"call-id\":\""
                + System.nanoTime()
                + "\",\"methodName\":\"mytgfs/internal/web.Bridge.Do\",\"args\":[{\"method\":\"POST\""
                + ",\"path\":" + json(path)
                + ",\"body\":" + json(body) + "}]},\"clientId\":\"mytgfs-android\"}";
        String resp = bridge.handleRuntimeCall(payload);
        if (resp == null) {
            Log.w(TAG, "runtime POST " + path + " -> null");
            return false;
        }
        try {
            org.json.JSONObject data = new org.json.JSONObject(resp).optJSONObject("data");
            int status = data == null ? 0 : data.optInt("status", 0);
            if (status != 200) {
                String detail = data == null ? resp : data.optString("body");
                Log.w(TAG, "runtime POST " + path + " -> " + status + " " + detail);
                return false;
            }
            return true;
        } catch (Exception e) {
            Log.w(TAG, "runtime POST parse failed: " + resp, e);
            return false;
        }
    }

    /**
     * ensureDir 在卷内目录里建一个子目录。
     *
     * 输入：vol 卷名；parentRel 相对选中根的父目录（顶层为空串）；name 目录名
     * 输出：true 建好或已存在；false 请求失败
     * 预期：走 /api/files/mkdir，路径按「目标目录 + 相对父目录」拼；重复建目录由
     *       Go 侧幂等处理，因此每次导入都安全
     */
    private boolean ensureDir(String vol, String parentRel, String name) {
        String dir = joinDir(folderDir, parentRel);
        String body = "{\"vol\":" + json(vol)
                + ",\"dir\":" + json(dir)
                + ",\"name\":" + json(name) + "}";
        return postViaRuntime("/api/files/mkdir", body);
    }

    /**
     * joinDir 拼卷内目录路径。
     *
     * 输入：base 卷内目录；rel 相对子目录（可为空、无前导斜杠）
     * 输出：以 "/" 开头的绝对卷内路径
     * 预期：base 为 "/" 时不产生双斜杠
     */
    private static String joinDir(String base, String rel) {
        String b = (base == null || base.isEmpty()) ? "/" : base;
        if (rel == null || rel.isEmpty()) return b;
        return b.endsWith("/") ? b + rel : b + "/" + rel;
    }

    /**
     * json 把字符串编码成 JSON 字面量。
     *
     * 输入：任意字符串（可为 null）
     * 输出：带引号的 JSON 字符串
     * 预期：转义引号、反斜杠、换行、制表符与控制字符，避免拼出坏 JSON
     */
    private static String json(String s) {
        StringBuilder sb = new StringBuilder("\"");
        for (char ch : (s == null ? "" : s).toCharArray()) {
            switch (ch) {
                case '"': sb.append("\\\""); break;
                case '\\': sb.append("\\\\"); break;
                case '\n': sb.append("\\n"); break;
                case '\r': sb.append("\\r"); break;
                case '\t': sb.append("\\t"); break;
                default:
                    if (ch < 0x20) sb.append(String.format("\\u%04x", (int) ch));
                    else sb.append(ch);
            }
        }
        return sb.append('"').toString();
    }

    private boolean adoptOne(String vol, String dir, android.net.Uri doc, String rel) {
        try (android.os.ParcelFileDescriptor pfd =
                     webView.getContext().getContentResolver().openFileDescriptor(doc, "r")) {
            if (pfd == null) return false;
            // 视频先抽一帧小图：Go 侧要按本地路径读取，失败就不带缩略图
            String thumb = makeThumb(doc);
            String q = "?vol=" + enc(vol)
                    + "&dir=" + enc(dir)
                    + "&rel=" + enc(rel)
                    + "&name=" + enc(rel)
                    + "&path=" + enc("/proc/self/fd/" + pfd.getFd())
                    + (thumb == null ? "" : "&thumb=" + enc(thumb));
            // 走 Bridge.Do RPC：asset 通道只转发 GET，POST 到不了 Go 的处理函数
            boolean ok = postViaRuntime("/api/upload/adopt" + q, "");
            if (!ok) {
                Log.w(TAG, "adopt rejected: " + rel);
                if (thumb != null) {
                    // 入队失败：小图留在 cache 里只会越积越多
                    //noinspection ResultOfMethodCallIgnored
                    new File(thumb).delete();
                }
            }
            return ok;
        } catch (Exception e) {
            Log.e(TAG, "adopt failed: " + rel, e);
            return false;
        }
    }

    /**
     * makeThumb 用系统解码器抽一帧，压成小 JPEG 交给 Go 当文档缩略图。
     *
     * 输入：doc 用户选中的内容 Uri
     * 输出：cache 里的 JPEG 绝对路径；非视频或解码失败返回 null
     * 预期：按 32KB 预算与最坏 bpp 算出缩放比例，质量 55%，只编码一次
     */
    private String makeThumb(android.net.Uri doc) {
        android.content.ContentResolver cr = webView.getContext().getContentResolver();
        String mime = null;
        try {
            mime = cr.getType(doc);
        } catch (Throwable ignored) {
        }
        if (mime == null || !mime.startsWith("video/")) return null;
        MediaMetadataRetriever r = new MediaMetadataRetriever();
        try {
            r.setDataSource(webView.getContext(), doc);
            Bitmap frame = r.getFrameAtTime(1_000_000L, MediaMetadataRetriever.OPTION_CLOSEST_SYNC);
            if (frame == null) frame = r.getFrameAtTime(0);
            if (frame == null) return null;
            int w = frame.getWidth();
            int h = frame.getHeight();
            if (w <= 0 || h <= 0) return null;
            byte[] best = null;
            try {
                // 缩放比例按 32KB 预算与最坏 bpp 反推，与网页端同一套算法
                double maxPixels = THUMB_BUDGET_BYTES * 8.0 / THUMB_WORST_BPP;
                double scale = Math.min(1.0, Math.sqrt(maxPixels / ((double) w * h)));
                int tw = Math.max(1, (int) Math.floor(w * scale));
                int th = Math.max(1, (int) Math.floor(h * scale));
                Bitmap small = (tw == w && th == h)
                        ? frame
                        : Bitmap.createScaledBitmap(frame, tw, th, true);
                best = compressJpeg(small, THUMB_QUALITY);
                if (small != frame) small.recycle();
            } finally {
                frame.recycle();
            }
            if (best == null) return null;
            File out = File.createTempFile("mytgfs-thumb-", ".jpg", webView.getContext().getCacheDir());
            try (FileOutputStream fos = new FileOutputStream(out)) {
                fos.write(best);
            }
            return out.getAbsolutePath();
        } catch (Throwable t) {
            Log.w(TAG, "thumb extract failed", t);
            return null;
        } finally {
            try {
                r.release();
            } catch (Throwable ignored) {
            }
        }
    }

    /**
     * compressJpeg 按给定质量把位图编码成 JPEG。
     *
     * 输入：bmp 位图；quality 0-100
     * 输出：JPEG 字节；编码失败返回 null
     */
    private static byte[] compressJpeg(Bitmap bmp, int quality) {
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        if (!bmp.compress(Bitmap.CompressFormat.JPEG, quality, out)) return null;
        return out.toByteArray();
    }

    private static String enc(String s) {
        try {
            return java.net.URLEncoder.encode(s == null ? "" : s, "UTF-8");
        } catch (Exception e) {
            return "";
        }
    }

    /**
     * finishFolderImport 把导入结果回传前端。
     *
     * 输入：ok 成功文件数
     * 输出：无
     * 预期：回调带成功数、失败数与失败相对路径数组，前端可逐条提示；
     *       数组用同样的 json 转义，避免文件名里的引号破坏脚本
     */
    private void finishFolderImport(int ok) {
        folderBusy = false;
        Log.i(TAG, "folder import done ok=" + ok + " failed=" + folderFailedNames.size());
        StringBuilder list = new StringBuilder("[");
        for (int i = 0; i < folderFailedNames.size(); i++) {
            if (i > 0) list.append(',');
            list.append(json(folderFailedNames.get(i)));
        }
        list.append(']');
        final String js = "window._mytgfsFolderImport && window._mytgfsFolderImport("
                + ok + "," + folderFailedNames.size() + "," + list + ");";
        webView.post(() -> webView.evaluateJavascript(js, null));
    }

    // ---- 频道头像：同样 fd 直读，不复制到缓存 ----

    public static final int COVER_PICK_REQUEST = 7005;

    private String coverVol = "";
    private boolean coverBusy = false;

    /** 打开系统图片选择器，选完直接把 fd 交给 Go 读。 */
    @JavascriptInterface
    public void pickCover(String vol) {
        if (coverBusy) return;
        coverBusy = true;
        coverVol = vol == null ? "" : vol;
        try {
            android.app.Activity act = (android.app.Activity) webView.getContext();
            android.content.Intent intent = new android.content.Intent(
                    android.content.Intent.ACTION_OPEN_DOCUMENT);
            intent.addCategory(android.content.Intent.CATEGORY_OPENABLE);
            intent.setType("image/*");
            act.startActivityForResult(intent, COVER_PICK_REQUEST);
        } catch (Exception e) {
            Log.e(TAG, "pickCover failed", e);
            finishCover(false);
        }
    }

    /** 由 MainActivity 在 onActivityResult 转发。 */
    public void onCoverPicked(final android.net.Uri uri) {
        if (uri == null) {
            finishCover(false);
            return;
        }
        executor.execute(() -> {
            boolean ok = false;
            try (android.os.ParcelFileDescriptor pfd = webView.getContext()
                    .getContentResolver().openFileDescriptor(uri, "r")) {
                if (pfd != null) {
                    String q = "?vol=" + enc(coverVol)
                            + "&src=" + enc("/proc/self/fd/" + pfd.getFd());
                    ok = bridge.serveAsset("/api/cover" + q, "POST", "{}") != null;
                }
            } catch (Exception e) {
                Log.e(TAG, "cover upload failed", e);
            }
            finishCover(ok);
        });
    }

    private void finishCover(boolean ok) {
        coverBusy = false;
        final String js = "window._mytgfsCoverPicked && window._mytgfsCoverPicked("
                + ok + ");";
        webView.post(() -> webView.evaluateJavascript(js, null));
    }
}
