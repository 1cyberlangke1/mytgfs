package com.wails.app;

import android.net.Uri;
import android.util.Log;
import android.webkit.WebResourceResponse;

import androidx.annotation.NonNull;
import androidx.annotation.Nullable;
import androidx.webkit.WebViewAssetLoader;

import java.io.ByteArrayInputStream;
import java.io.InputStream;
import java.util.HashMap;
import java.util.Map;

/**
 * WailsPathHandler implements WebViewAssetLoader.PathHandler to serve assets
 * from the Go asset server. This allows the WebView to load assets without
 * using a network server, similar to iOS's WKURLSchemeHandler.
 */
public class WailsPathHandler implements WebViewAssetLoader.PathHandler {
    private static final String TAG = "WailsPathHandler";
    private static final boolean DEBUG = BuildConfig.DEBUG;

    private final WailsBridge bridge;

    public WailsPathHandler(WailsBridge bridge) {
        this.bridge = bridge;
    }

    @Nullable
    @Override
    public WebResourceResponse handle(@NonNull String path) {
        if (DEBUG) Log.d(TAG, "Handling path: " + path);

        // 根路径不要改写成 /index.html：Go 侧是 http.FileServer，
        // /index.html 会被 301 到 ./，而 bridge 只把 200 当成功，
        // 结果 WebView 认为资源不存在、转去真网络（ERR_CONNECTION_REFUSED）。
        // 直接把 "/" 交给面板 handler，它会返回 index.html。
        String assetPath = path.isEmpty() ? "/" : path;

        // Get asset from Go
        byte[] data = bridge.serveAsset(assetPath, "GET", "{}");

        if (data == null || data.length == 0) {
            Log.w(TAG, "Asset not found: " + assetPath);
            return null; // Return null to let WebView handle 404
        }

        // Determine MIME type。Go 侧资源服务器对 "/" 只给 application/octet-stream，
        // WebView 会当成下载而不渲染；模块脚本还会因 MIME 不是 JS 被直接拦下。
        // 所以按扩展名给确定值，认不出的再问 Go。
        String mimeType = mimeFor(assetPath);
        if ("application/octet-stream".equals(mimeType)) {
            String fromGo = bridge.getAssetMimeType(assetPath);
            if (fromGo != null && !fromGo.isEmpty()) {
                mimeType = fromGo;
            }
        }
        if (DEBUG) Log.d(TAG, "Serving " + assetPath + " with type " + mimeType + " (" + data.length + " bytes)");

        // Create response
        InputStream inputStream = new ByteArrayInputStream(data);
        Map<String, String> headers = new HashMap<>();
        headers.put("Access-Control-Allow-Origin", "*");
        headers.put("Cache-Control", "no-cache");

        return new WebResourceResponse(
                mimeType,
                "UTF-8",
                200,
                "OK",
                headers,
                inputStream
        );
    }

    // mimeFor 按扩展名给前端产物确定 MIME。
    //
    // 输入：资源路径（"/" 或 /assets/xxx）
    // 输出：MIME 字符串；未知扩展名返回 application/octet-stream
    // 预期：text/javascript 是 ES module 的硬性要求，错一个字符就整页白屏
    private static String mimeFor(String path) {
        String p = path.toLowerCase(java.util.Locale.ROOT);
        if (p.equals("/") || p.endsWith(".html")) return "text/html";
        if (p.endsWith(".js") || p.endsWith(".mjs")) return "text/javascript";
        if (p.endsWith(".css")) return "text/css";
        if (p.endsWith(".json") || p.endsWith(".webmanifest") || p.endsWith(".map")) {
            return "application/json";
        }
        if (p.endsWith(".svg")) return "image/svg+xml";
        if (p.endsWith(".png")) return "image/png";
        if (p.endsWith(".jpg") || p.endsWith(".jpeg")) return "image/jpeg";
        if (p.endsWith(".webp")) return "image/webp";
        if (p.endsWith(".ico")) return "image/x-icon";
        if (p.endsWith(".woff2")) return "font/woff2";
        if (p.endsWith(".woff")) return "font/woff";
        if (p.endsWith(".wasm")) return "application/wasm";
        return "application/octet-stream";
    }
}
