package main

import (
	"github.com/egoist/mygo"
)

// AppService 暴露应用自身的信息与桌面集成能力。
// Every exported method here is bound to the frontend, so helpers stay unexported.
type AppService struct{}

// Version 返回应用版本号（来自打包信息，即 mygo.config.ts 里的 version）。
func (AppService) Version() string {
	return mygo.App.Version()
}

// OpenURL 用系统默认应用打开 URL：设置页「关于」里的 GitHub 主页链接
// 经它在用户的外部浏览器里打开，而不是应用内 webview。
func (AppService) OpenURL(url string) error {
	return mygo.Shell.OpenExternal(url)
}
