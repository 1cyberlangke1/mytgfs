package web

// 临时本地 Web 面板：查看 daemon 状态、编辑配置。
//
// 只应绑定 127.0.0.1；进程退出即关闭。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"

	"mytgfs/internal/atomicfile"
	"mytgfs/internal/daemon"
	"mytgfs/internal/ipc"
)

// Serve 启动 Web 面板，阻塞到 ctx 取消。
//
// 输入：ctx；addr 监听地址（127.0.0.1:port）；configPath 配置文件
// 输出：error
// 预期：ctx 取消时优雅关闭
func Serve(ctx context.Context, addr, configPath string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handlePage)
	mux.HandleFunc("/api/status", handleStatus)
	mux.HandleFunc("/api/config", handleConfig(configPath))

	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func handlePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, pageHTML)
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	st, err := daemon.ReadState()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "daemon 未运行"})
		return
	}
	data, err := ipc.Call(r.Context(), st.Addr, st.Token, "status", nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func handleConfig(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			b, err := os.ReadFile(path)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write(b)
		case http.MethodPut:
			b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			if err := atomicfile.WriteFile(path, b, 0o600); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"ok": "saved"})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

const pageHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>mytgfs</title>
<style>
body{font-family:system-ui,sans-serif;max-width:900px;margin:2rem auto;padding:0 1rem}
pre{background:#f4f4f4;padding:1rem;border-radius:6px;overflow:auto}
textarea{width:100%;font-family:ui-monospace,monospace;font-size:13px}
button{padding:.5rem 1rem;margin-top:.5rem}
</style>
</head>
<body>
<h1>mytgfs</h1>
<h2>daemon 状态</h2>
<pre id="status">加载中…</pre>
<h2>配置</h2>
<textarea id="cfg" rows="24"></textarea>
<button onclick="save()">保存配置</button>
<pre id="msg"></pre>
<script>
fetch('/api/status').then(r=>r.text()).then(t=>document.getElementById('status').textContent=t)
fetch('/api/config').then(r=>r.text()).then(t=>document.getElementById('cfg').value=t)
function save(){
  fetch('/api/config',{method:'PUT',body:document.getElementById('cfg').value})
    .then(r=>r.text())
    .then(t=>document.getElementById('msg').textContent=t)
}
</script>
</body>
</html>`
