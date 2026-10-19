// chgksuite is the desktop app: a window of tabs, each a native webview. The
// Local tab is xy in local mode, served from this process with no port; every
// other tab is an xy server the user has added.
package main

import (
	"embed"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"xy/localserver"
)

//go:embed shell
var shellFS embed.FS

func main() {
	config, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	dir := filepath.Join(config, "chgksuite")
	local, err := localserver.Open(filepath.Join(dir, "local"))
	if err != nil {
		log.Fatal(err)
	}
	defer local.Close()
	shell, err := openShell(filepath.Join(dir, "servers.json"), local)
	if err != nil {
		log.Fatal(err)
	}
	local.SetFilePicker(shell.pickFile)

	// The tab strip and the Wails runtime have paths of their own; everything
	// else is the Local tab's xy. The fork refuses a panel's request for
	// /wails/ and any request from a page it did not serve.
	mux := http.NewServeMux()
	mux.Handle("/shell/", http.FileServerFS(shellFS))
	mux.Handle("/wails/", application.BundledAssetFileServer(shellFS))
	mux.Handle("/", local.Handler())

	app := application.New(application.Options{
		Name:     "chgksuite",
		Assets:   application.AssetOptions{Handler: mux},
		Services: []application.Service{application.NewService(shell)},
		Mac:      application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	shell.window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "chgksuite",
		Width:  1280,
		Height: 860,
		URL:    "/shell/",
		// The tab strip is the title bar, beside the traffic lights.
		Mac: application.MacWindow{TitleBar: application.MacTitleBarHidden},
	})
	centreTrafficLights(shell.window)
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
