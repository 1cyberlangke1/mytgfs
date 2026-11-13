package web

// 账号接口：填 api_id/api_hash/bot token，登录 bot 与 user。
//
// 凭证内容永不回传，只回「有没有」；登录成功回身份，失败回可读原因。

import (
	"context"
	"net/http"
	"strings"
	"time"

	"mytgfs/internal/authsvc"
	"mytgfs/internal/i18n"
)

// loginTimeout 是单次登录请求的上限：扫码/2FA 之外正常都远低于这个值。
const loginTimeout = 90 * time.Second

// handleAPICredentials 读取或保存凭证。
//
// 输入：GET 读状态；PUT {"api_id","api_hash","bot_token"} 写文件
// 输出：authsvc.Status；保存后附 restart_required
// 预期：空字符串表示「不修改」；能力未注入 501
func handleAPICredentials(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.Auth == nil {
			replyJSONError(w, http.StatusNotImplemented, i18n.T("err.login_unavailable"))
			return
		}
		switch r.Method {
		case http.MethodGet:
			replyJSON(w, http.StatusOK, opts.Auth.Status())
		case http.MethodPut, http.MethodPost:
			var in struct {
				APIID    int    `json:"api_id"`
				APIHash  string `json:"api_hash"`
				BotToken string `json:"bot_token"`
			}
			if !decodeBody(w, r, &in) {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			defer cancel()
			st, err := opts.Auth.SaveCredentials(ctx, in.APIID, in.APIHash, in.BotToken)
			if err != nil {
				replyJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			replyJSON(w, http.StatusOK, map[string]any{
				"status": st,
				// 运行中的 bot 连接仍用旧凭证，改完要重启才生效
				"restart_required": true,
			})
		default:
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
		}
	}
}

// handleAPILoginBot 用 token 登录 bot。
//
// 输入：POST {"token"}（可为空，表示用已保存的 token）
// 输出：bot 身份
// 预期：登录成功后 token 才落盘
func handleAPILoginBot(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
			return
		}
		if opts.Auth == nil {
			replyJSONError(w, http.StatusNotImplemented, i18n.T("err.login_unavailable"))
			return
		}
		var in struct {
			Token string `json:"token"`
		}
		if !decodeBody(w, r, &in) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), loginTimeout)
		defer cancel()
		idn, err := opts.Auth.LoginBot(ctx, in.Token)
		if err != nil {
			replyJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		replyJSON(w, http.StatusOK, map[string]any{"user": idn, "restart_required": true})
	}
}

// handleAPIUserStart 给手机号发登录验证码。
//
// 输入：POST {"phone"}
// 输出：{"phone_code_hash","timeout"}
// 预期：下一步提交验证码时把 hash 原样带回
func handleAPIUserStart(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
			return
		}
		if opts.Auth == nil {
			replyJSONError(w, http.StatusNotImplemented, i18n.T("err.login_unavailable"))
			return
		}
		var in struct {
			Phone string `json:"phone"`
		}
		if !decodeBody(w, r, &in) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), loginTimeout)
		defer cancel()
		req, err := opts.Auth.StartUserLogin(ctx, in.Phone)
		if err != nil {
			replyJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		replyJSON(w, http.StatusOK, req)
	}
}

// handleAPIUserFinish 提交验证码；需要 2FA 时回 need_password。
//
// 输入：POST {"phone","code","phone_code_hash","password"}
// 输出：{"need_password":bool,"user":{...}}
// 预期：need_password 为 true 时前端显示密码框再调一次
func handleAPIUserFinish(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
			return
		}
		if opts.Auth == nil {
			replyJSONError(w, http.StatusNotImplemented, i18n.T("err.login_unavailable"))
			return
		}
		var in authsvc.FinishRequest
		if !decodeBody(w, r, &in) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), loginTimeout)
		defer cancel()
		res, err := opts.Auth.FinishUserLogin(ctx, in)
		if err != nil {
			replyJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		replyJSON(w, http.StatusOK, res)
	}
}

// handleAPIIdentity 读取已保存会话的身份。
//
// 输入：GET ?kind=bot|user
// 输出：{"user":{...}}
// 预期：会话缺失/失效回 400，附可读原因
func handleAPIIdentity(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
			return
		}
		if opts.Auth == nil {
			replyJSONError(w, http.StatusNotImplemented, i18n.T("err.login_unavailable"))
			return
		}
		kind := strings.TrimSpace(r.URL.Query().Get("kind"))
		ctx, cancel := context.WithTimeout(r.Context(), loginTimeout)
		defer cancel()
		idn, err := opts.Auth.Identity(ctx, kind != "user")
		if err != nil {
			replyJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		replyJSON(w, http.StatusOK, map[string]any{"user": idn})
	}
}
