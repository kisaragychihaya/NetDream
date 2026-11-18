package main

import (
	"log"

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
	mygo.Bind(RequestService{}, WorkspaceService{}, SocketService{})

	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
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
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
