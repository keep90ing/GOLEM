// internal/daemon/daemon.go
package daemon

import (
	"gocker/internal/config"
	"gocker/internal/container"
	"gocker/internal/image"
	"log"
	"net"
	"os"
)

type Server struct {
	ContainerManager *container.Manager
	ImageManager     *image.Manager
}

func NewServer(cm *container.Manager, im *image.Manager) *Server {
	return &Server{
		ContainerManager: cm,
		ImageManager:     im,
	}
}

func (s *Server) Run() error {
	// 清理舊的 socket 檔案
	if err := os.RemoveAll(config.SocketPath); err != nil {
		return err
	}

	// 監聽 socket
	listener, err := net.Listen("unix", config.SocketPath)
	if err != nil {
		return err
	}
	defer listener.Close()

	// 設定 socket 檔案的權限
	if err := os.Chmod(config.SocketPath, 0660); err != nil {
		return err
	}

	log.Println("gocker-daemon 已啟動，正在監聽", config.SocketPath)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("接受連線失敗: %v", err)
			continue
		}
		go s.handleConnection(conn)
	}
}
