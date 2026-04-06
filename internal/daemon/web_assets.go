package daemon

import (
	"embed"
	"io/fs"
	"net/http"
	"sync"
)

//go:embed all:web
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
		fileServer := http.FileServer(http.FS(sub))
		daemonWebHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			setNoStoreHeaders(w.Header())
			fileServer.ServeHTTP(w, r)
		})
	})
	return daemonWebHandler
}

func setNoStoreHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store, max-age=0")
	header.Set("Pragma", "no-cache")
	header.Set("Expires", "0")
}
