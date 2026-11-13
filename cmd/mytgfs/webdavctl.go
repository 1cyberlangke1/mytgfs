package main

// 面板里的 WebDAV 挂盘开关（`mytgfs web` 模式）。
//
// 启停实现与托盘共用 internal/davsvc；这里只负责挑卷、写配置、回报状态。

import (
	"fmt"
	"os"

	"mytgfs/internal/config"
	"mytgfs/internal/davsvc"
	"mytgfs/internal/i18n"
	"mytgfs/internal/web"
)

// newMountControl 组装面板用的挂盘控制器。
//
// 输入：cfgPath 配置文件；vols 已打开的卷（必须包含 davfs）
// 输出：服务实例与 web 控制器；调用方负责在退出时 Stop
// 预期：
//   - 每次开启都重新读配置，[webdav].volume 改动立即生效
//   - 启停成功后把 enabled 写回配置，下次启动沿用
func newMountControl(cfgPath string, vols map[string]*webVolume) (*davsvc.Service, *web.MountControl) {
	svc := &davsvc.Service{}
	state := func() web.MountState {
		st := svc.State()
		return web.MountState{
			Available: len(vols) > 0,
			Enabled:   st.Enabled,
			Addr:      st.Addr,
			Volume:    st.Volume,
		}
	}
	set := func(on bool) error {
		cur, err := config.Load(cfgPath)
		if err != nil {
			return fmt.Errorf("%s: %w", i18n.T("err.config_load"), err)
		}
		if on {
			if !svc.State().Enabled {
				vol, ok := cur.WebDAV.Target(cur.Volumes)
				if !ok {
					return fmt.Errorf("%s", i18n.T("err.webdav_no_volume"))
				}
				v := vols[vol.Name]
				if v == nil || v.davfs == nil {
					return fmt.Errorf("%s: %s", i18n.T("err.webdav_no_volume"), vol.Name)
				}
				// 写请求串行化，避免抢 Telegram 发消息配额
				if _, err := svc.Start(cur.WebDAV.ListenAddr(), vol.Name, v.davfs); err != nil {
					return fmt.Errorf("%s: %w", i18n.T("err.webdav_start"), err)
				}
			}
		} else if err := svc.Stop(); err != nil {
			return fmt.Errorf("%s: %w", i18n.T("err.webdav_stop"), err)
		}
		cur.WebDAV.Enabled = on
		if werr := config.SetWebDAV(cfgPath, cur.WebDAV); werr != nil {
			// 服务已经启停成功，配置失败只提醒
			fmt.Fprintf(os.Stderr, "%s: %v\n", i18n.T("err.webdav_config"), werr)
		}
		return nil
	}
	return svc, &web.MountControl{
		Status: state,
		// 先启停再取状态：state() 必须先反映新状态，消息才不会说反
		Enable:  func() (web.MountState, error) { err := set(true); return state(), err },
		Disable: func() (web.MountState, error) { err := set(false); return state(), err },
	}
}
