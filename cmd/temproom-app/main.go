package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"temproom/internal/app"
	"temproom/internal/browser"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:dist
var distFS embed.FS

func main() {
	// Remove profiles (and their Chrome) left behind by a crash or force-quit.
	if n := browser.SweepStale(); n > 0 {
		log.Printf("removed %d stale temporary Chrome profile(s)", n)
	}
	svc := app.NewService()
	defer svc.Close()
	apiHandler := NewAPIHandler(svc)

	subFS, err := fs.Sub(distFS, "dist")
	if err != nil {
		log.Fatalf("failed to create sub filesystem: %v", err)
	}

	assetServer := application.AssetFileServerFS(subFS)

	apiMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api") {
				apiHandler.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	wailsApp := application.New(application.Options{
		Name:        "TempRoom",
		Description: "Temporary Network Environment Sharing and Browser Collaboration Platform",
		Services: []application.Service{
			application.NewService(svc),
		},
		Mac: application.MacOptions{
			// Closing the window quits the app, which ends the session (ServiceShutdown).
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		Assets: application.AssetOptions{
			Handler:    assetServer,
			Middleware: apiMiddleware,
		},
	})

	wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:         "TempRoom",
		Width:         480,
		Height:        640,
		MinWidth:      440,
		MinHeight:     580,
		DisableResize: false,
		Mac: application.MacWindow{
			TitleBar:                application.MacTitleBarHiddenInset,
			InvisibleTitleBarHeight: 50,
		},
	})

	if err := wailsApp.Run(); err != nil {
		log.Fatal(err)
	}
}
