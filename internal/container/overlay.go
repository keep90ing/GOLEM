// internal/container/overlay.go
package container

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"gocker/internal/config"
	"gocker/internal/image"
)

// SetupOverlayFS 設定 OverlayFS
func SetupOverlayFS(imageName string) (string, error) {
	// 產生容器 ID
	containerID, err := generateContainerID()
	if err != nil {
		return "", fmt.Errorf("產生容器 ID 失敗: %v", err)
	}

	// 檢查映像是否存在
	manager := image.NewManager()
	if !manager.Exists(imageName) {
		return "", fmt.Errorf("映像不存在: %s (請先執行 'gocker pull %s')", imageName, imageName)
	}

	imageTarPath := manager.GetImagePath(imageName)
	containerDir := filepath.Join(config.ContainersDir, containerID)

	// 建立 OverlayFS 目錄結構
	lowerDir := filepath.Join(containerDir, "lower")
	upperDir := filepath.Join(containerDir, "upper")
	workDir := filepath.Join(containerDir, "work")
	mergedDir := filepath.Join(containerDir, "merged")

	// 建立所有必要的目錄
	for _, dir := range []string{lowerDir, upperDir, workDir, mergedDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("建立目錄 %s 失敗: %v", dir, err)
		}
	}

	// 解壓縮映像到 lowerDir
	if err := extractImage(imageTarPath, lowerDir); err != nil {
		return "", fmt.Errorf("解壓縮映像失敗: %v", err)
	}

	// 掛載 OverlayFS
	if err := mountOverlay(lowerDir, upperDir, workDir, mergedDir); err != nil {
		return "", fmt.Errorf("掛載 OverlayFS 失敗: %v", err)
	}

	fmt.Printf("成功建立並掛載 OverlayFS 於: %s\n", mergedDir)
	return mergedDir, nil
}

// generateContainerID 產生隨機的容器 ID
func generateContainerID() (string, error) {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", bytes), nil
}

// extractImage 解壓縮映像檔案
func extractImage(tarPath, targetDir string) error {
	fmt.Printf("正在解壓縮映像: %s\n", tarPath)

	cmd := exec.Command("tar", "-xf", tarPath, "-C", targetDir)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	if err := cmd.Run(); err != nil {
		// 提供更詳細的錯誤資訊
		fmt.Printf("解壓縮失敗，檢查 tar 檔案內容...\n")
		checkCmd := exec.Command("tar", "-tf", tarPath)
		checkCmd.Stdout = os.Stdout
		checkCmd.Stderr = os.Stderr
		checkCmd.Run()

		return fmt.Errorf("解壓縮映像 '%s' 失敗: %v", tarPath, err)
	}

	return nil
}

// mountOverlay 掛載 OverlayFS
func mountOverlay(lowerDir, upperDir, workDir, mergedDir string) error {
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lowerDir, upperDir, workDir)
	return syscall.Mount("overlay", mergedDir, "overlay", 0, opts)
}
