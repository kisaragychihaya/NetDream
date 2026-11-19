package main

import (
	"log"
	// Embedded IANA time zone data: user machines have no GOROOT zoneinfo,
	// and nd.time.format/parse need named zones there.
	_ "time/tzdata"

	"github.com/egoist/mygo"
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
	mygo.Bind(RequestService{}, WorkspaceService{}, SocketService{}, AppService{})

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
		// 记住窗口并让有未保存改动时的关闭先问过页面（见 close.go）。
		guardWindow(win)
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
