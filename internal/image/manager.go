// internal/image/manager.go
package image

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gocker/internal/config"
	"gocker/internal/types"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/sirupsen/logrus"
)

// Manager 映像管理器
type Manager struct {
	storageDir string
}

// NewManager 建立新的映像管理器
func NewManager() *Manager {
	manager := &Manager{
		storageDir: config.ImagesDir,
	}

	// 確保儲存目錄存在
	os.MkdirAll(manager.storageDir, 0755)

	return manager
}

// Pull 拉取映像
func (m *Manager) PullImage(imageName string) error {
	log := logrus.WithField("image", imageName)
	log.Info("開始從遠端倉庫拉取映像...")

	// 1. 使用 crane 拉取映像
	img, err := crane.Pull(imageName)
	if err != nil {
		return fmt.Errorf("crane pull 失敗: %w", err)
	}

	// 2. 獲取映像的唯一 Digest (摘要)，並以此作為 ImageID
	digest, err := img.Digest()
	if err != nil {
		return fmt.Errorf("獲取映像 digest 失敗: %w", err)
	}
	imageID := strings.TrimPrefix(digest.String(), "sha256:")[:12]
	log = log.WithField("imageID", imageID)

	// 3. 建立該映像的專屬儲存目錄
	imageStorePath := filepath.Join(m.storageDir, imageID)
	if err := os.MkdirAll(imageStorePath, 0755); err != nil {
		return fmt.Errorf("建立映像儲存目錄 %s 失敗: %w", imageStorePath, err)
	}

	// 4. 將映像匯出為 tarball
	imageTarPath := filepath.Join(imageStorePath, "image.tar")
	log.Infof("正在將映像匯出至 %s", imageTarPath)
	f, err := os.Create(imageTarPath)
	if err != nil {
		return fmt.Errorf("建立映像 tarball 失敗: %w", err)
	}
	if err := crane.Export(img, f); err != nil {
		f.Close()
		return fmt.Errorf("匯出映像失敗: %w", err)
	}
	f.Close()

	// 5. 建立 rootfs 目錄並解壓縮 tarball
	rootfsPath := filepath.Join(imageStorePath, "rootfs")
	log.Infof("正在將映像解壓縮至 %s", rootfsPath)
	if err := Untar(imageTarPath, rootfsPath); err != nil {
		return fmt.Errorf("解壓縮映像失敗: %w", err)
	}

	// 6. ★★★ 寫入 manifest.json ★★★
	log.Info("正在更新 manifest.json...")
	if err := m.updateManifest(imageName, imageID); err != nil {
		return fmt.Errorf("更新 manifest.json 失敗: %w", err)
	}

	log.Info("映像處理完成")
	return nil
}

// updateManifest 讀取、更新並寫回 manifest.json
func (m *Manager) updateManifest(repoTag, imageID string) error {
	manifestPath := filepath.Join(m.storageDir, "manifest.json")

	// 讀取現有 manifest，如果不存在則建立一個空的
	var manifests []types.ImageManifest
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("讀取 manifest.json 失敗: %w", err)
		}
		// 檔案不存在是正常情況，我們將建立一個新的
	} else {
		if err := json.Unmarshal(data, &manifests); err != nil {
			return fmt.Errorf("解析 manifest.json 失敗: %w", err)
		}
	}

	// 檢查 tag 是否已存在，如果存在則更新，否則新增
	found := false
	for i := range manifests {
		if manifests[i].RepoTag == repoTag {
			manifests[i].ImageID = imageID
			found = true
			break
		}
	}
	if !found {
		manifests = append(manifests, types.ImageManifest{
			RepoTag: repoTag,
			ImageID: imageID,
		})
	}

	// 將更新後的內容寫回檔案
	newData, err := json.MarshalIndent(manifests, "", "    ")
	if err != nil {
		return fmt.Errorf("序列化 manifest 失敗: %w", err)
	}

	return os.WriteFile(manifestPath, newData, 0644)
}

// Untar 解壓縮一個 tar 檔案到指定目錄
func Untar(tarPath, destPath string) error {
	_ = os.MkdirAll(destPath, 0755)

	file, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer file.Close()

	tarReader := tar.NewReader(file)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destPath, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			_ = os.MkdirAll(target, os.FileMode(header.Mode))
		case tar.TypeReg:
			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			_, _ = io.Copy(outFile, tarReader)
			outFile.Close()
		case tar.TypeSymlink:
			_ = os.Symlink(header.Linkname, target)
		}
	}
	return nil
}

// List 列出本地映像
func (m *Manager) ListImages() ([]string, error) {
	// 讀取 manifest 檔案的內容
	content, err := os.ReadFile(config.ManifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	// 解析 JSON 內容
	var entries []types.ImageManifest
	if err := json.Unmarshal(content, &entries); err != nil {
		return nil, err
	}

	// 提取並回傳映像名稱列表
	var images []string
	for _, entry := range entries {
		images = append(images, entry.RepoTag)
	}

	return images, nil
}

// GetImagePath 取得映像的檔案路徑
func (m *Manager) GetImagePath(imageName string) string {
	return filepath.Join(m.storageDir, m.sanitizeImageName(imageName)+".tar")
}

// Exists 檢查映像是否存在
func (m *Manager) Exists(imageName string) bool {
	path := m.GetImagePath(imageName)
	_, err := os.Stat(path)
	return err == nil
}

// sanitizeImageName 清理映像名稱作為檔案名稱
func (m *Manager) sanitizeImageName(imageName string) string {
	return strings.Replace(imageName, ":", "_", 1)
}

// ListContainers 讀取所有容器的設定檔並回傳
func ListContainers() ([]*types.ContainerInfo, error) {
	files, err := os.ReadDir(config.ContainerStoragePath)
	if err != nil {
		return nil, fmt.Errorf("讀取容器儲存目錄失敗: %w", err)
	}

	var containers []*types.ContainerInfo
	for _, file := range files {
		if !file.IsDir() {
			continue
		}
		containerID := file.Name()
		configFilePath := filepath.Join(config.ContainerStoragePath, containerID, "config.json")

		data, err := os.ReadFile(configFilePath)
		if err != nil {
			// 如果 config.json 讀取失敗，就跳過這個目錄
			continue
		}

		var info types.ContainerInfo
		if err := json.Unmarshal(data, &info); err != nil {
			// 如果 json 解析失敗，也跳過
			continue
		}
		containers = append(containers, &info)
	}
	return containers, nil
}
