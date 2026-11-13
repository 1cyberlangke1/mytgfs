package web

// 面板里的「挂载为网络驱动器」开关。
//
// WebDAV 默认关闭：面板本身已经能增删改查，只有需要资源管理器盘符时
// 才打开。开关状态写回 config.toml 的 [webdav].enabled，下次启动沿用。

import (
	"net/http"

	"mytgfs/internal/eventbus"
	"mytgfs/internal/i18n"
)

// MountState 是 WebDAV 服务的当前状态快照。
type MountState struct {
	// Available 为 false 表示当前模式不提供挂盘（例如只读面板）
	Available bool
	Enabled   bool
	Addr      string
	Volume    string
}

// MountControl 由调用方注入实际的启停能力；nil 表示不提供。
type MountControl struct {
	Status  func() MountState
	Enable  func() (MountState, error)
	Disable func() (MountState, error)
}

// mountData 是开关卡片的模板数据。
type mountData struct {
	Labels pageLabels
	MountState
	Message string
	MsgKind string
}

// handleFragMount 处理开关请求（form: on=0|1）；GET 只返回当前状态。
//
// 输入：GET 无参数；POST on=1 打开、其它值关闭
// 输出：#mountbox 片段
// 预期：失败也返回 200 + 错误文案，htmx 才会就地替换
func handleFragMount(ctl *MountControl, bus *eventbus.Bus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if ctl == nil || ctl.Status == nil {
				http.Error(w, i18n.T("web.mount_unavailable"), http.StatusNotImplemented)
				return
			}
			renderMount(w, mountData{Labels: loadLabels(false), MountState: ctl.Status()})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if ctl == nil {
			http.Error(w, i18n.T("web.mount_unavailable"), http.StatusNotImplemented)
			return
		}
		if err := r.ParseForm(); err != nil {
			renderMount(w, mountData{Labels: loadLabels(false), MountState: MountState{}, Message: err.Error(), MsgKind: "err"})
			return
		}
		var (
			st  MountState
			err error
		)
		if r.FormValue("on") == "1" {
			st, err = ctl.Enable()
		} else {
			st, err = ctl.Disable()
		}
		data := mountData{Labels: loadLabels(false), MountState: st}
		if err != nil {
			data.MsgKind = "err"
			data.Message = err.Error()
		} else {
			data.MsgKind = "ok"
			if st.Enabled {
				data.Message = i18n.T("web.mount_enabled", st.Addr, st.Volume)
			} else {
				data.Message = i18n.T("web.mount_disabled")
			}
			// 其它标签页与托盘菜单都跟着这次变更走
			if bus != nil {
				bus.Publish("mount")
				bus.Publish("config")
			}
		}
		renderMount(w, data)
	}
}

// renderMount 输出开关卡片片段。
func renderMount(w http.ResponseWriter, data mountData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := pageTmpl.ExecuteTemplate(w, "mountbox", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
