package tray

// 系统托盘：常驻图标 + 右键菜单。
//
// 菜单语义（与用户约定一致）：
//   - 打开面板：启动（如未启动）Web 面板并在浏览器打开
//   - 关闭面板：关闭 Web 面板，托盘与挂载继续运行
//   - 卸载并退出：卸载所有磁盘后结束整个程序（卸载与退出是一件事）

import (
	"fmt"
	"os"
	"sync"

	"fyne.io/systray"

	"mytgfs/internal/i18n"
)

// Actions 是托盘菜单触发的动作，由调用方注入；nil 表示忽略。
type Actions struct {
	OpenWeb  func()
	CloseWeb func()
	Quit     func()

	// AutostartEnabled 是启动时从系统读到的开机自启状态。
	AutostartEnabled bool
	// SetAutostart 打开/关闭开机自启；返回 error 时菜单保持原状态。
	SetAutostart func(bool) error
}

var (
	mu       sync.Mutex
	statusIt *systray.MenuItem
)

// SetStatus 更新托盘里的状态行（如「已连接」「重连中」）。
//
// 输入：s 状态文本
// 输出：无
// 预期：托盘尚未就绪时只记下，就绪后由 Run 写入初始状态
func SetStatus(s string) {
	mu.Lock()
	it := statusIt
	mu.Unlock()
	if it != nil {
		it.SetTitle(i18n.T("tray.status", s))
	}
}

// Run 在当前线程运行托盘主循环，阻塞直到退出。
//
// 输入：a 菜单动作；initial 初始状态文本
// 输出：无（systray.Quit 后返回）
// 预期：必须在主 goroutine 调用；图标与菜单就绪后开始分发点击事件
func Run(a Actions, initial string) {
	systray.Run(func() { onReady(a, initial) }, func() {})
}

func onReady(a Actions, initial string) {
	if icon := iconBytes(); icon != nil {
		systray.SetIcon(icon)
	}
	systray.SetTooltip(i18n.T("tray.tooltip"))

	mStatus := systray.AddMenuItem(i18n.T("tray.status", initial), i18n.T("tray.status.tip"))
	mStatus.Disable()
	mu.Lock()
	statusIt = mStatus
	mu.Unlock()

	systray.AddSeparator()
	mOpen := systray.AddMenuItem(i18n.T("tray.menu.open"), i18n.T("tray.menu.open.tip"))
	mClose := systray.AddMenuItem(i18n.T("tray.menu.close"), i18n.T("tray.menu.close.tip"))
	mAuto := systray.AddMenuItemCheckbox(
		i18n.T("tray.menu.autostart"),
		i18n.T("tray.menu.autostart.tip"),
		a.AutostartEnabled,
	)
	systray.AddSeparator()
	mQuit := systray.AddMenuItem(i18n.T("tray.menu.quit"), i18n.T("tray.menu.quit.tip"))

	// 左键单击直接打开面板，常见托盘习惯
	systray.SetOnTapped(func() {
		if a.OpenWeb != nil {
			a.OpenWeb()
		}
	})

	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				if a.OpenWeb != nil {
					a.OpenWeb()
				}
			case <-mClose.ClickedCh:
				if a.CloseWeb != nil {
					a.CloseWeb()
				}
			case <-mAuto.ClickedCh:
				if a.SetAutostart == nil {
					continue
				}
				next := !mAuto.Checked()
				if err := a.SetAutostart(next); err != nil {
					fmt.Fprintf(os.Stderr, "autostart: %v\n", err)
					continue
				}
				// 菜单勾选状态跟随系统写入结果，不提前翻转
				if next {
					mAuto.Check()
				} else {
					mAuto.Uncheck()
				}
			case <-mQuit.ClickedCh:
				if a.Quit != nil {
					a.Quit()
				}
				systray.Quit()
				return
			}
		}
	}()
}
