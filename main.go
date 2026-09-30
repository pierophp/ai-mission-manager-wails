package main

import (
	"embed"
	"log"
	"os"

	"github.com/piero/ai-mission-manager-wails/backend"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	runtime, err := backend.OpenDefaultRuntime()
	if err != nil {
		log.Fatal(err)
	}
	defer runtime.Close()
	commands := &backend.CommandService{Runtime: runtime}
	platform := &backend.PlatformService{}
	app := application.New(application.Options{
		Name:        "AI Mission Manager",
		Description: "Desktop mission manager",
		Services: []application.Service{
			application.NewService(commands),
			application.NewService(platform),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.piero.aimissionmanager",
		},
	})
	runtime.SetEventEmitter(backend.EventEmitterFunc(func(name string, payload any) {
		app.Event.Emit(name, payload)
	}))

	platform.OpenURLFunc = app.Browser.OpenURL
	platform.RevealItemFunc = func(path string) error {
		return app.Env.OpenFileManager(path, true)
	}
	platform.OpenDirectoryFunc = func(options backend.OpenDirectoryOptions) (any, error) {
		dialog := app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
			CanChooseDirectories:    true,
			CanChooseFiles:          false,
			AllowsMultipleSelection: options.Multiple,
			Title:                   options.Title,
		})
		if options.Multiple {
			paths, err := dialog.PromptForMultipleSelection()
			if err != nil {
				return nil, err
			}
			if len(paths) == 0 {
				return nil, nil
			}
			return paths, nil
		}
		path, err := dialog.PromptForSingleSelection()
		if err != nil {
			return nil, err
		}
		if path == "" {
			return nil, nil
		}
		return path, nil
	}

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "AI Mission Manager",
		Width:            920,
		Height:           720,
		MinWidth:         680,
		MinHeight:        520,
		BackgroundColour: application.NewRGB(247, 244, 238),
		URL:              "/",
	})
	if app.Env.Info().Debug && os.Getenv("AI_MISSION_MANAGER_OPEN_DEVTOOLS") == "true" {
		window.OpenDevTools()
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
