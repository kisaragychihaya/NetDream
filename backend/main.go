package main

import (
	"log"
	// Embedded IANA time zone data: user machines have no GOROOT zoneinfo,
	// and nd.time.format/parse need named zones there.
	_ "time/tzdata"

	"github.com/egoist/mygo"
	// MyGo 自带控件/菜单的翻译：随 SetLanguages 声明的语言生效，
	// 不引入时 MyGo 自身字符串（对话框按钮等）始终是英文。
	_ "github.com/egoist/mygo/locales/ja"
	_ "github.com/egoist/mygo/locales/zh-Hans"
)

// The window the page opens in.
const (
	windowTitle     = "NetDream"
	windowWidth     = 1280
	windowHeight    = 800
	windowMinWidth  = 960
	windowMinHeight = 600
)

func main() {
	mygo.Bind(RequestService{}, WorkspaceService{}, SocketService{}, GrpcService{}, AppService{}, IdentityService{})

	// 应用界面支持的语言（第一个是回退语言）。不调 SetLanguages 时
	// MyGo 只认英语，App.Locale()（即前端 mygo.locale）在任何语言的
	// 系统上都解析成 "en"，「跟随系统」会恒显示英文。
	mygo.App.SetLanguages("zh-CN", "en-US", "ja")

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:           windowTitle,
			Width:           windowWidth,
			Height:          windowHeight,
			MinWidth:        windowMinWidth,
			MinHeight:       windowMinHeight,
			BackgroundColor: "light-dark(#f6f7f9, #0f1115)", // the page's --bg in frontend/src/style.css
			// Opens where the user left it last time.
			StateKey: "main",
			// The frontend: devUrl during `mygo dev`, frontendDist
			// embedded by `mygo build` otherwise (see mygo.config.ts).
			URL: "/",
		})
		// 激活 mygo 原生文件拖放的路径投递（需至少一个 Go 侧监听器）；
		// 路径经 mygo:file-drop 页面事件由前端 mygo-runtime 的 onFileDrop 接收。
		win.OnFileDrop(func(*mygo.FileDropEvent) {})
		if !mygo.IsDev() {
			// 发布版屏蔽 WebView 自带的右键菜单（Reload/Back/Forward 等）。
			// 开发者工具在发布版本就关闭：DevToolsAuto（默认）在
			// `mygo build` 产物里（production ldflag）自动禁用。
			win.Page().OnContextMenu(func(e *mygo.ContextMenuEvent) {
				e.PreventDefault()
			})
		}
		// 记住窗口并让有未保存改动时的关闭先问过页面（见 close.go）。
		guardWindow(win)
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
