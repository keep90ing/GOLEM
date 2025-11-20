package main

import (
	"log"
	"os"

	"gocker/internal/container"
	"gocker/internal/daemon"
	"gocker/internal/image"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "init" {

		log.Println("--- Daemon in init mode ---")
		if err := container.InitContainer(); err != nil {
			log.Fatalf("子行程初始化失敗: %v", err)
		}
		return
	}

	log.Println("--- Daemon in server mode ---")

	containerManager := container.NewManager()
	imageManager := image.NewManager()

	server := daemon.NewServer(containerManager, imageManager)

	if err := server.Run(); err != nil {
		log.Fatalf("Daemon 啟動失敗: %v", err)
	}
}
