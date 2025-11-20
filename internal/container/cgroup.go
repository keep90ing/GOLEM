package container

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gocker/internal/config"
	"gocker/internal/types"
	"gocker/pkg"

	"github.com/sirupsen/logrus"
)

func cgroupPath(info *types.ContainerInfo) (string, string, error) {
    parent := filepath.Join(config.CgroupRoot, config.CgroupName)
    // 1) Preferred: container ID directory
    idPath := filepath.Join(parent, info.ID)
    if st, err := os.Stat(idPath); err == nil && st.IsDir() {
        return idPath, "id", nil
    }
    // 2) Legacy fallback: PID directory (deprecated)
    if info.PID > 0 {
        legacy := filepath.Join(parent, fmt.Sprintf("%d", info.PID))
        if st, err := os.Stat(legacy); err == nil && st.IsDir() {
            return legacy, "pid", nil
        }
    }
    return "", "", fmt.Errorf("cgroup path not found for container %s", info.ID)
}

// SetupCgroup 由父行程呼叫，為指定的 PID 建立一個獨立的 cgroup 並設定資源限制
func (m *Manager) SetupCgroup(limits types.ContainerLimits, pid int, containerID string) (string, error) {
	parentCgroupPath := filepath.Join(config.CgroupRoot, config.CgroupName)
	log := logrus.WithField("parentCgroup", parentCgroupPath)

	// 1. 確保父 cgroup 目錄存在
	if err := os.MkdirAll(parentCgroupPath, 0755); err != nil {
		return "", fmt.Errorf("建立父 cgroup 目錄 %s 失敗: %w", parentCgroupPath, err)
	}

	// 2. 啟用必要的 cgroup 控制器
	controllers := "+cpu +memory +pids"
	controlFilePath := filepath.Join(parentCgroupPath, "cgroup.subtree_control")
	log.Infof("正在啟用 cgroup 控制器: %s", controllers)
	if err := os.WriteFile(controlFilePath, []byte(controllers), 0644); err != nil {
		log.Warnf("啟用 cgroup 控制器可能失敗 (可忽略): %v", err)
	}

	// 3. 為每個容器建立一個獨立的 cgroup 路徑
	containerCgroupPath := filepath.Join(parentCgroupPath, containerID)
	log = logrus.WithField("cgroupPath", containerCgroupPath)

	log.Info("正在建立容器的 cgroup...")
	if err := os.MkdirAll(containerCgroupPath, 0755); err != nil {
		return "", fmt.Errorf("建立 cgroup 目錄 %s 失敗: %w", containerCgroupPath, err)
	}

	// 4. 設定具體的資源限制
	log.Info("正在設定容器的資源限制...")
	if err := m.setResourceLimits(containerCgroupPath, limits); err != nil {
		// 如果設定失敗，清理已建立的目錄
		_ = m.CleanupCgroup(containerCgroupPath)
		return "", fmt.Errorf("設定資源限制失敗: %w", err)
	}

	// 5. 將子行程 PID 加入 cgroup
	procsPath := filepath.Join(containerCgroupPath, "cgroup.procs")
	log.Infof("正在將 PID %d 加入 cgroup...", pid)
	if err := os.WriteFile(procsPath, []byte(strconv.Itoa(pid)), 0644); err != nil {
		_ = m.CleanupCgroup(containerCgroupPath)
		return "", fmt.Errorf("將 PID 加入 cgroup.procs 失敗: %w", err)
	}

	log.Info("Cgroup 設定成功")
	// 6. 回傳建立的 cgroup 路徑，以便後續清理
	return containerCgroupPath, nil
}

// CleanupCgroup 負責在容器停止後，清理其對應的 cgroup 目錄
func (m *Manager) CleanupCgroup(cgroupPath string) error {
	log := logrus.WithField("cgroupPath", cgroupPath)
	log.Info("正在清理 cgroup...")

	// 遞迴刪除目錄
	if err := os.RemoveAll(cgroupPath); err != nil {
		return fmt.Errorf("移除 cgroup 目錄 %s 失敗: %w", cgroupPath, err)
	}

	log.Info("成功清理 cgroup")
	return nil
}

func (m *Manager) AdjustResourceLimits(identifier string, limits types.ContainerLimits) error {
	info, err := findContainerInfo(identifier)
	if err != nil {
		return fmt.Errorf("找不到容器資訊: %w", err)
	}

	if info.Status != types.Running {
		return fmt.Errorf("容器 %s 不在運行狀態，目前狀態為: %s", identifier, info.Status)
	}

	logrus.Infof("調整容器 %s (PID %d) 的資源限制", identifier, info.PID)

	// Keep original limits if new limits are invalid
	originalLimits := info.Limits
	if limits.CPULimit == config.InvalidLimit {
		limits.CPULimit = originalLimits.CPULimit
	}
	if limits.MemoryLimit == config.InvalidLimit {
		limits.MemoryLimit = originalLimits.MemoryLimit
	}
	if limits.PidsLimit == config.InvalidLimit {
		limits.PidsLimit = originalLimits.PidsLimit
	}

	containerCgroupPath, mode, err := cgroupPath(info)
	if err != nil {
		return err
	}
	log := logrus.WithFields(logrus.Fields{
		"cgroupPath": containerCgroupPath,
		"resolver":   mode, // "id" 或 "pid"
	})
	if mode == "pid" {
		log.Warn("Using legacy PID-based cgroup path; But lets use container ID based path next time, mate!")
	}
	// we don't need to check if the path exists, cgroupPath already did that
	log.Info("正在調整容器的資源限制...")
    if err := m.setResourceLimits(containerCgroupPath, limits); err != nil {
        return fmt.Errorf("調整資源限制失敗: %w", err)
    }

	log.Info("成功調整容器的資源限制")
	// 更新 config.json 中的限制資訊
	info.Limits = limits
	containerDir := filepath.Join(config.ContainerStoragePath, info.ID)
	if err := pkg.WriteContainerInfo(containerDir, info); err != nil {
		return fmt.Errorf("更新容器設定檔失敗: %w", err)
	}
	log.Info("已更新容器的限制資訊至配置檔")
	return nil
}

// setResourceLimits 用來寫入 cgroup 限制檔案
func (m *Manager) setResourceLimits(cgroupPath string, limits types.ContainerLimits) error {
	// 設定 CPU 限制 (cpu.max)
	if limits.CPULimit > 0 {
		// CPULimit: 0.5 -> 50000 100000 (50% quota in a 100ms period)
		cpuQuota := limits.CPULimit * 100000
		cpuPeriod := 100000
		cpuLimitString := fmt.Sprintf("%d %d", cpuQuota, cpuPeriod)
		cpuMaxPath := filepath.Join(cgroupPath, "cpu.max")
		if err := os.WriteFile(cpuMaxPath, []byte(cpuLimitString), 0644); err != nil {
			return fmt.Errorf("寫入 cpu.max 失敗: %w", err)
		}
	}

	// 設定記憶體限制 (memory.max)
	if limits.MemoryLimit > 0 {
		memMaxPath := filepath.Join(cgroupPath, "memory.max")
		memLimitString := strconv.Itoa(limits.MemoryLimit)
		if err := os.WriteFile(memMaxPath, []byte(memLimitString), 0644); err != nil {
			return fmt.Errorf("寫入 memory.max 失敗: %w", err)
		}
	}

	// 設定 PID 限制 (pids.max)
	if limits.PidsLimit > 0 {
		pidsMaxPath := filepath.Join(cgroupPath, "pids.max")
		pidsLimitString := strconv.Itoa(limits.PidsLimit)
		if err := os.WriteFile(pidsMaxPath, []byte(pidsLimitString), 0644); err != nil {
			return fmt.Errorf("寫入 pids.max 失敗: %w", err)
		}
	}

	return nil
}
