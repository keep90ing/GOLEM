// gocker/internal/remove.go
package internal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"gocker/internal/config"
	"gocker/internal/image"
	"gocker/internal/types"

	"github.com/sirupsen/logrus"
)

type Remover struct {
	reader *bufio.Reader
}

func NewRemover() *Remover {
	return &Remover{
		reader: bufio.NewReader(os.Stdin),
	}
}

func (r *Remover) RemoveContainer(containerID string) error {
	return removeContainer(containerID)
}

func (r *Remover) RemoveAllContainers() error {
	containers, err := image.ListContainers()
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	if len(containers) == 0 {
		fmt.Println("No containers to remove.")
		return nil
	}

	fmt.Printf("Are you sure you want to remove all %d containers? (y/n): ", len(containers))
	input, err := r.reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}

	choice := strings.TrimSpace(strings.ToLower(input))
	if choice != "y" && choice != "yes" {
		fmt.Println("Cancelled removal of all containers.")
		return nil
	}

	removed := 0
	for _, c := range containers {
		if err := r.RemoveContainer(c.ID); err != nil {
			logrus.Errorf("Failed to remove container %s (%s): %v", c.Name, c.ID, err)
			continue
		}
		fmt.Printf("Removed container %s (%s)\n", c.Name, c.ID)
		removed++
	}

	if removed == 0 {
		fmt.Println("No containers were removed.")
	} else {
		fmt.Printf("Successfully removed %d containers.\n", removed)
	}

	return nil
}

func removeContainer(containerID string) error {
	// 1. 直接定位到指定容器的目錄
	containerDir := filepath.Join(config.ContainerStoragePath, containerID)
	configFilePath := filepath.Join(containerDir, "config.json")

	// 2. 讀取該容器的設定檔
	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return fmt.Errorf("找不到容器 %s 的設定檔: %w", containerID, err)
	}

	var info types.ContainerInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return fmt.Errorf("解析容器 %s 的設定檔失敗: %w", containerID, err)
	}

	// 3. 安全檢查：不允許刪除正在運行的容器
	if info.Status == types.Running {
		return fmt.Errorf("無法刪除正在運行的容器 %s，請先停止它", containerID)
	}

	// 4. 解除掛載 (Unmount)
	if info.MountPoint != "" {
		logrus.Infof("正在解除掛載 %s", info.MountPoint)
		if err := syscall.Unmount(info.MountPoint, 0); err != nil {
			logrus.Warnf("解除掛載 %s 失敗: %v", info.MountPoint, err)
		}
	}

	// 5. 刪除cgroup
	cgroupPath := filepath.Join(config.CgroupRoot, config.CgroupName, info.ID)
	logrus.Infof("正在清理 Cgroup %s", cgroupPath)

	if err := os.RemoveAll(cgroupPath); err != nil {
		return fmt.Errorf("移除 cgroup 目錄 %s 失敗: %w", cgroupPath, err)
	}
	logrus.Info("成功清理 cgroup")

	// 6. 刪除整個容器目錄
	logrus.Infof("正在刪除容器目錄 %s", containerDir)
	if err := os.RemoveAll(containerDir); err != nil {
		return fmt.Errorf("刪除容器目錄 %s 失敗: %w", containerDir, err)
	}

	logrus.Infof("成功刪除容器 %s", containerID)
	return nil
}
