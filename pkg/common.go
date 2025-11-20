package pkg

import (
	"encoding/json"
	"fmt"
	"os"

	"path/filepath"
	"strings"

	"gocker/internal/image"
	"gocker/internal/types"
)

func Parse(input string) (string, string) {
	s := strings.Split(input, ":")
	if len(s) == 1 {
		return s[0], "latest"
	}
	if len(s) == 2 {
		return s[0], s[1]
	}
	return "", ""
}

// writeContainerInfo 將容器資訊寫回檔案
func WriteContainerInfo(containerDir string, info *types.ContainerInfo) error {
	configFilePath := filepath.Join(containerDir, "config.json")
	file, err := os.Create(configFilePath)
	if err != nil {
		return fmt.Errorf("建立 config.json 失敗: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "    ") // 格式化 JSON
	return encoder.Encode(info)
}

func GetSelfExecutablePath() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("無法獲取執行檔路徑: %w", err)
	}

	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return "", fmt.Errorf("無法解析執行檔路徑的符號連結: %w", err)
	}
	return exePath, nil
}

func ResolveContainer(identifier string) (*types.ContainerInfo, error) {
	containers, err := image.ListContainers()
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	var matches []*types.ContainerInfo
	for _, c := range containers {
		if c.ID == identifier || c.Name == identifier || strings.HasPrefix(c.ID, identifier) {
			matches = append(matches, c)
		}
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("cannot find container: %s", identifier)
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("found multiple containers matching %s, please specify a more precise ID", identifier)
	}
}
