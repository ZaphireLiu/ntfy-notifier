//go:build windows

package tray

import (
	"context"
	"fmt"
	"time"

	"fyne.io/systray"
)

// Provider supplies status text for the tray.
type Provider interface {
	StatusText() string
	StatusDetail() string
	Recent() []string
}

// Actions are the callbacks invoked from the menu.
type Actions struct {
	Pause        func(time.Duration)
	Resume       func()
	Reload       func()
	OpenConfig   func()
	OpenScripts  func()
	OpenLogs     func()
	Autostart    bool
	SetAutostart func(bool) error
	Quit         func()
}

// Run blocks until the tray is closed or ctx is cancelled.
func Run(ctx context.Context, p Provider, a Actions, icon []byte) {
	go func() {
		<-ctx.Done()
		systray.Quit()
	}()
	systray.Run(func() {
		if len(icon) > 0 {
			systray.SetIcon(icon)
		}
		systray.SetTitle("ntfy")
		systray.SetTooltip("ntfy Notifier")

		status := systray.AddMenuItem("连接中…", "")
		status.Disable()
		systray.AddSeparator()

		pause15 := systray.AddMenuItem("暂停 15 分钟", "")
		pause60 := systray.AddMenuItem("暂停 1 小时", "")
		pauseDay := systray.AddMenuItem("暂停 24 小时", "")
		resume := systray.AddMenuItem("恢复通知", "")
		systray.AddSeparator()

		reload := systray.AddMenuItem("重载脚本", "")
		openConfig := systray.AddMenuItem("打开配置目录", "")
		openScripts := systray.AddMenuItem("打开脚本目录", "")
		openLogs := systray.AddMenuItem("打开日志目录", "")
		systray.AddSeparator()

		autostart := systray.AddMenuItemCheckbox("开机自启", "", a.Autostart)
		systray.AddSeparator()

		recent := systray.AddMenuItem("最近事件", "")
		recentItems := make([]*systray.MenuItem, 8)
		for i := range recentItems {
			recentItems[i] = recent.AddSubMenuItem("-", "")
			recentItems[i].Disable()
		}
		systray.AddSeparator()
		quit := systray.AddMenuItem("退出", "")

		on := func(item *systray.MenuItem, fn func()) {
			go func() {
				for range item.ClickedCh {
					fn()
				}
			}()
		}
		on(pause15, func() {
			if a.Pause != nil {
				a.Pause(15 * time.Minute)
			}
		})
		on(pause60, func() {
			if a.Pause != nil {
				a.Pause(time.Hour)
			}
		})
		on(pauseDay, func() {
			if a.Pause != nil {
				a.Pause(24 * time.Hour)
			}
		})
		on(resume, func() {
			if a.Resume != nil {
				a.Resume()
			}
		})
		on(reload, func() {
			if a.Reload != nil {
				a.Reload()
			}
		})
		on(openConfig, func() {
			if a.OpenConfig != nil {
				a.OpenConfig()
			}
		})
		on(openScripts, func() {
			if a.OpenScripts != nil {
				a.OpenScripts()
			}
		})
		on(openLogs, func() {
			if a.OpenLogs != nil {
				a.OpenLogs()
			}
		})
		on(quit, func() {
			if a.Quit != nil {
				a.Quit()
			}
			systray.Quit()
		})
		on(autostart, func() {
			want := !autostart.Checked()
			if a.SetAutostart != nil {
				if err := a.SetAutostart(want); err != nil {
					return
				}
			}
			if want {
				autostart.Check()
			} else {
				autostart.Uncheck()
			}
		})

		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			lastStatus := ""
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				if text := p.StatusText(); text != lastStatus {
					status.SetTitle(text)
					lastStatus = text
				}
				systray.SetTooltip(p.StatusDetail())
				if events := p.Recent(); len(events) > 0 {
					for i := range recentItems {
						if i < len(events) {
							recentItems[i].SetTitle(fmt.Sprintf("%d. %s", i+1, events[i]))
						} else {
							recentItems[i].SetTitle("-")
						}
					}
				}
			}
		}()
	}, func() {
		if a.Quit != nil {
			a.Quit()
		}
	})
}
