package web

// 开机自启接口：面板上的开关直接读写系统注册项，不经过配置文件。

import (
	"fmt"
	"net/http"

	"mytgfs/internal/i18n"
)

// AutostartControl 是开机自启的读写接口，由 internal/autostart 实现。
//
// 输入：无 / 目标开关状态
// 输出：当前是否开启 / 写入错误
// 预期：状态以系统注册项为唯一事实来源，面板不缓存、不写 config.toml
type AutostartControl interface {
	Enabled() (bool, error)
	Set(on bool) error
}

// handleAPIAutostart 读写开机自启。
//
// 输入：GET 无参数；POST {"enabled":bool}
// 输出：GET/POST 均回 {"enabled":bool}
// 预期：未注入控制时回 501，前端据此隐藏入口
func handleAPIAutostart(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctl := opts.Autostart
		if ctl == nil {
			replyJSONError(w, http.StatusNotImplemented, i18n.T("err.autostart_unsupported"))
			return
		}
		switch r.Method {
		case http.MethodGet:
			on, err := ctl.Enabled()
			if err != nil {
				replyJSONError(w, http.StatusInternalServerError, fmt.Sprintf("%s: %v", i18n.T("err.autostart_read"), err))
				return
			}
			replyJSON(w, http.StatusOK, map[string]bool{"enabled": on})
		case http.MethodPost:
			var in struct {
				Enabled bool `json:"enabled"`
			}
			if !decodeBody(w, r, &in) {
				return
			}
			if err := ctl.Set(in.Enabled); err != nil {
				replyJSONError(w, http.StatusInternalServerError, fmt.Sprintf("%s: %v", i18n.T("err.autostart_write"), err))
				return
			}
			replyJSON(w, http.StatusOK, map[string]bool{"enabled": in.Enabled})
		default:
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
		}
	}
}
