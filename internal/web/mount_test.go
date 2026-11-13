package web

// 挂盘开关 handler 单测：不启真实服务，只验证状态回传与表单方向。

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestMount 造一个可控的开关控制器。
//
// calls 记录每次调用是开还是关，便于断言「勾选状态取反」这件事。
func newTestMount(calls *[]bool, fail error) *MountControl {
	return &MountControl{
		Status: func() MountState {
			return MountState{Available: true, Enabled: false, Addr: "127.0.0.1:8080", Volume: "demo"}
		},
		Enable: func() (MountState, error) {
			*calls = append(*calls, true)
			if fail != nil {
				return MountState{Available: true}, fail
			}
			return MountState{Available: true, Enabled: true, Addr: "127.0.0.1:8080", Volume: "demo"}, nil
		},
		Disable: func() (MountState, error) {
			*calls = append(*calls, false)
			if fail != nil {
				return MountState{Available: true}, fail
			}
			return MountState{Available: true, Enabled: false}, nil
		},
	}
}

// TestFragMountNil 不支持挂盘时返回 501。
func TestFragMountNil(t *testing.T) {
	rec := httptest.NewRecorder()
	handleFragMount(nil, nil)(rec, httptest.NewRequest(http.MethodPost, "/frag/mount", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("code = %d", rec.Code)
	}
}

// TestFragMountEnable 打开后片段里应显示地址与「停止」表单。
func TestFragMountEnable(t *testing.T) {
	var calls []bool
	rec := httptest.NewRecorder()
	req := formRequest("/frag/mount", "on=1")
	handleFragMount(newTestMount(&calls, nil), nil)(rec, req)
	if len(calls) != 1 || !calls[0] {
		t.Fatalf("calls = %v", calls)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "127.0.0.1:8080") {
		t.Errorf("缺少监听地址: %s", body)
	}
	// 已开启时表单的隐藏值应为 0，即下次点击是关闭
	if !strings.Contains(body, `name="on" value="0"`) {
		t.Errorf("表单方向错误: %s", body)
	}
}

// TestFragMountDisable 关闭后表单方向应变成打开。
func TestFragMountDisable(t *testing.T) {
	var calls []bool
	rec := httptest.NewRecorder()
	handleFragMount(newTestMount(&calls, nil), nil)(rec, formRequest("/frag/mount", "on=0"))
	if len(calls) != 1 || calls[0] {
		t.Fatalf("calls = %v", calls)
	}
	if !strings.Contains(rec.Body.String(), `name="on" value="1"`) {
		t.Errorf("表单方向错误: %s", rec.Body.String())
	}
}

// TestFragMountError 启停失败要返回错误样式，不能假装成功。
func TestFragMountError(t *testing.T) {
	var calls []bool
	rec := httptest.NewRecorder()
	handleFragMount(newTestMount(&calls, errors.New("port busy")), nil)(rec, formRequest("/frag/mount", "on=1"))
	if !strings.Contains(rec.Body.String(), `class="err"`) {
		t.Errorf("缺少错误样式: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "port busy") {
		t.Errorf("缺少错误详情: %s", rec.Body.String())
	}
}
