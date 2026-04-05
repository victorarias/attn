package daemon

import (
	"embed"
	"io/fs"
	"net/http"
	"sync"
)

//go:embed web/* web/vendor/*
var daemonWebAssets embed.FS

var (
	daemonWebHandlerOnce sync.Once
	daemonWebHandler     http.Handler
)

func daemonWebStaticHandler() http.Handler {
	daemonWebHandlerOnce.Do(func() {
		sub, err := fs.Sub(daemonWebAssets, "web")
		if err != nil {
			panic("embedded web assets missing: " + err.Error())
		}
		daemonWebHandler = http.FileServer(http.FS(sub))
	})
	return daemonWebHandler
}
