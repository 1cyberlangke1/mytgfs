package web

// 卷管理接口：面板里新建/删除卷，与 CLI 的 `volume create|delete` 等价。
//
// 建卷需要 user 会话（只有用户能建频道、拉 bot 进频道），
// 因此具体实现在 trayapp / cmd 注入，本文件只负责 HTTP 契约。

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"mytgfs/internal/channel"
	"mytgfs/internal/config"
	"mytgfs/internal/i18n"
)

// handleAPIChannels 列出 user 会话可管理的频道，供「添加已有频道」下拉选择。
//
// 输入：GET
// 输出：{"channels":[{id,chat_id,title,username,creator,admin}]}
// 预期：能力未注入 501；user 会话缺失或失效时 400 并给出人话原因
func handleAPIChannels(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
			return
		}
		if opts.Channels == nil {
			replyJSONError(w, http.StatusNotImplemented, i18n.T("err.channels_unavailable"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		items, err := opts.Channels(ctx)
		if err != nil {
			replyJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if items == nil {
			items = []channel.Item{}
		}
		replyJSON(w, http.StatusOK, map[string]any{"channels": items})
	}
}

// handleAPIAttachVolume 把已有频道挂成卷。
//
// 输入：POST {"name","chat_id"}（chat_id 可为字符串或数字）
// 输出：卷信息；失败 400/500
// 预期：能力未注入 501；卷名重复 409
func handleAPIAttachVolume(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
			return
		}
		if opts.AttachVolume == nil {
			replyJSONError(w, http.StatusNotImplemented, i18n.T("err.volume_attach_unavailable"))
			return
		}
		var in struct {
			Name   string `json:"name"`
			ChatID int64  `json:"chat_id"`
		}
		if !decodeBody(w, r, &in) {
			return
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			replyJSONError(w, http.StatusBadRequest, i18n.T("err.volume_name_required"))
			return
		}
		if !validVolumeName(name) {
			replyJSONError(w, http.StatusBadRequest, i18n.T("err.volume_name_invalid"))
			return
		}
		res, err := opts.AttachVolume(r.Context(), name, in.ChatID)
		if err != nil {
			if errors.Is(err, config.ErrVolumeExists) {
				replyJSONError(w, http.StatusConflict, i18n.T("err.volume_exists", name))
				return
			}
			replyJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		replyJSON(w, http.StatusOK, res)
	}
}

// handleAPIVolumes 新建或删除卷。
//
// 输入：
//   - POST {"name","title"}
//   - DELETE ?name=
//
// 输出：创建返回卷信息；删除返回 {"removed":true}
// 预期：能力未注入时返回 501；重名 409；参数缺失 400
func handleAPIVolumes(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			if opts.CreateVolume == nil {
				replyJSONError(w, http.StatusNotImplemented, i18n.T("err.volume_create_unavailable"))
				return
			}
			var in struct {
				Name  string `json:"name"`
				Title string `json:"title"`
			}
			if !decodeBody(w, r, &in) {
				return
			}
			name := strings.TrimSpace(in.Name)
			if name == "" {
				replyJSONError(w, http.StatusBadRequest, i18n.T("err.volume_name_required"))
				return
			}
			// 卷名要拼进 index-<name>.db，只允许安全字符
			if !validVolumeName(name) {
				replyJSONError(w, http.StatusBadRequest, i18n.T("err.volume_name_invalid"))
				return
			}
			res, err := opts.CreateVolume(r.Context(), name, strings.TrimSpace(in.Title))
			if err != nil {
				if errors.Is(err, config.ErrVolumeExists) {
					replyJSONError(w, http.StatusConflict, i18n.T("err.volume_exists", name))
					return
				}
				replyJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			replyJSON(w, http.StatusOK, res)
		case http.MethodDelete:
			if opts.DeleteVolume == nil {
				replyJSONError(w, http.StatusNotImplemented, i18n.T("err.volume_delete_unavailable"))
				return
			}
			name := strings.TrimSpace(r.URL.Query().Get("name"))
			if name == "" {
				replyJSONError(w, http.StatusBadRequest, i18n.T("err.volume_name_required"))
				return
			}
			if err := opts.DeleteVolume(r.Context(), name); err != nil {
				replyJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			replyJSON(w, http.StatusOK, map[string]any{"removed": true, "name": name})
		default:
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
		}
	}
}

// validVolumeName 校验卷名。
//
// 输入：name 用户输入
// 输出：true 表示可安全用于文件名与配置
// 预期：字母/数字/下划线/中划线，长度 1..32
func validVolumeName(name string) bool {
	if len(name) == 0 || len(name) > 32 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
