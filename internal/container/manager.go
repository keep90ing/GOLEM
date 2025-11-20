// internal/container/manager.go
package container

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/sirupsen/logrus"

	"gocker/internal/config"
	"gocker/internal/network"
	"gocker/internal/types"
	"gocker/pkg"
)

// Manager 負責所有容器的生命週期管理
type Manager struct {
	StoragePath string
}

type StartOptions struct {
	Attach bool
	Tty    bool
	Conn   io.ReadWriteCloser
}

// NewManager 建立一個新的容器管理器
func NewManager() *Manager {
	return &Manager{
		StoragePath: config.ContainerStoragePath,
	}
}

func (m *Manager) CreateAndRun(req *types.RunRequest) (string, error) {
	rootCgroupProcs := "/sys/fs/cgroup/cgroup.procs"
	if err := os.WriteFile(rootCgroupProcs, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		return "", fmt.Errorf("無法將父行程移至 cgroup root: %w", err)
	}

	// 1. 產生一個隨機的容器 ID
	randBytes := make([]byte, 12)
	if _, err := rand.Read(randBytes); err != nil {
		return "", fmt.Errorf("無法產生容器 ID: %w", err)
	}
	containerID := hex.EncodeToString(randBytes)

	if req.ContainerName == "" {
		req.ContainerName = containerID
	}

	log := logrus.WithFields(logrus.Fields{
		"containerID": containerID,
		"image":       fmt.Sprintf("%s:%s", req.ImageName, req.ImageTag),
		"name":        req.ContainerName,
	})

	log.Info("父行程: 準備啟動容器...")

	// 2. 建立容器的工作目錄
	containerDir := filepath.Join(config.ContainerStoragePath, containerID)
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		return "", fmt.Errorf("建立容器目錄失敗: %w", err)
	}
	mountPoint := filepath.Join(containerDir, "rootfs")
	req.MountPoint = mountPoint

	// 3. 建立並寫入初始的 config.json
	info := &types.ContainerInfo{
		ID:         containerID,
		Name:       req.ContainerName,
		Command:    req.ContainerCommand,
		Status:     types.Created,
		CreatedAt:  time.Now(),
		Image:      fmt.Sprintf("%s:%s", req.ImageName, req.ImageTag),
		MountPoint: mountPoint,
		Limits:     req.ContainerLimits,
	}
	if err := pkg.WriteContainerInfo(containerDir, info); err != nil {
		return "", fmt.Errorf("寫入容器設定檔失敗: %w", err)
	}

	// 4. 建立匿名管道用於父子行程通信
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		return "", fmt.Errorf("建立管道失敗: %w", err)
	}
	defer readPipe.Close()

	// 5. 準備啟動子行程的命令
	cmd := exec.Command("/proc/self/exe", "init")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWNET,
	}
	cmd.Dir = "/"
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{readPipe}

	// 6. 啟動子行程
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("啟動子行程失敗: %w", err)
	}
	childPid := cmd.Process.Pid
	log.Infof("父行程: 子行程已啟動，PID 為 %d", childPid)

	// 7. 父行程為子行程設定外部環境
	// 7.1 設定 cgroup
	log.Info("父行程: 設定 cgroup 資源限制...")
	cgroupPath, err := m.SetupCgroup(req.ContainerLimits, childPid, containerID)
	if err != nil {
		_ = cmd.Process.Kill()
		return "", fmt.Errorf("設定 cgroup 失敗: %w", err)
	}

	// 7.2 設定網路，並得到 peerName
	log.Info("父行程: 設定容器網路...")
	peerName, err := network.SetupVeth(childPid)
	if err != nil {
		_ = cmd.Process.Kill()
		return "", fmt.Errorf("設定網路失敗: %w", err)
	}
	req.VethPeerName = peerName

	// 8. 將 req 物件寫入管道，通知子行程繼續
	defer writePipe.Close()
	encoder := json.NewEncoder(writePipe)
	if err := encoder.Encode(req); err != nil {
		_ = cmd.Process.Kill()
		return "", fmt.Errorf("父行程: 向管道寫入配置失敗: %v", err)
	}

	// 9. 更新 config.json，寫入 PID 並將狀態改為 Running
	info.PID = childPid
	info.Status = types.Running
	if err := pkg.WriteContainerInfo(containerDir, info); err != nil {
		log.Warnf("更新容器狀態為 Running 失敗: %v", err)
	}

	// 10. 等待容器行程結束
	go func() {
		defer func() {
			// 確保讀取端也被關閉
			_ = readPipe.Close()
			log.Infof("Daemon: 容器 %s (PID: %d) 已退出", req.ContainerName, childPid)

			// 11. 容器結束後，再次更新狀態
			info.PID = 0
			info.Status = types.Stopped
			info.FinishedAt = time.Now()
			if err := pkg.WriteContainerInfo(containerDir, info); err != nil {
				log.Warnf("更新容器狀態為 Stopped 失敗: %v", err)
			}

			// 12. 清理 cgroup
			log.Info("Daemon: 清理 cgroup...")
			_ = m.CleanupCgroup(cgroupPath)
		}()

		if err := cmd.Wait(); err != nil {
			log.Warnf("Daemon: 等待容器行程結束時發生錯誤: %v", err)
		}
	}()

	return containerID, nil
}
func (m *Manager) StopContainer(identifier string) error {
	// 1. 查找容器資訊
	info, err := m.GetInfo(identifier)
	if err != nil {
		return err
	}

	// 2. 檢查容器狀態
	if info.Status != types.Running {
		return fmt.Errorf("容器 %s 不在運行狀態，目前狀態為: %s", identifier, info.Status)
	}

	pid := info.PID
	log := logrus.WithFields(logrus.Fields{
		"containerID": info.ID,
		"pid":         pid,
	})

	log.Infof("正在停止容器...")

	// 3. 發送 SIGTERM 信號
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if err.Error() != "no such process" {
			return fmt.Errorf("向容器行程 %d 發送 SIGTERM 信號失敗: %w", pid, err)
		}
		log.Warnf("行程 %d 已不存在，但仍將狀態標記為 Stopped", pid)
	}

	// 4. 更新容器狀態
	info.Status = types.Stopped
	info.PID = 0 // 清理 PID
	info.FinishedAt = time.Now()

	containerDir := filepath.Join(config.ContainerStoragePath, info.ID)
	if err := pkg.WriteContainerInfo(containerDir, info); err != nil {
		return fmt.Errorf("更新容器狀態為 Stopped 失敗: %w", err)
	}

	if err := network.CleanupContainerNetwork(info.ID); err != nil {
		log.Warnf("釋放容器網路資源失敗: %v", err)
	}

	log.Infof("容器 %s 已成功停止", identifier)
	return nil
}

func (m *Manager) StopAllContainers() error {
	logrus.Info("正在停止所有運行中的容器...")

	containers, err := m.List()
	if err != nil {
		return fmt.Errorf("獲取容器列表失敗: %v", err)
	}

	stoppedCount := 0
	for _, c := range containers {
		if c.Status == types.Running {
			logrus.Infof("正在停止容器 %s (%s)", c.Name, c.ID[:12])

			if err := m.StopContainer(c.ID); err != nil {
				logrus.Warnf("停止容器 %s 失敗: %v", c.ID, err)
			} else {
				stoppedCount++
			}
		}
	}

	if stoppedCount == 0 {
		fmt.Println("沒有正在運行的容器。")
	} else {
		fmt.Printf("成功停止 %d 個容器。\n", stoppedCount)
	}

	return nil
}

func (m *Manager) List() ([]*types.ContainerInfo, error) {
	files, err := os.ReadDir(m.StoragePath)
	if err != nil {
		return nil, fmt.Errorf("讀取容器儲存目錄 %s 失敗: %w", m.StoragePath, err)
	}

	var containers []*types.ContainerInfo
	for _, file := range files {
		if !file.IsDir() {
			continue
		}
		containerID := file.Name()
		configPath := filepath.Join(m.StoragePath, containerID, "config.json")

		data, err := os.ReadFile(configPath)
		if err != nil {
			logrus.Warnf("讀取容器設定檔 %s 失敗: %v", configPath, err)
			continue
		}

		var info types.ContainerInfo
		if err := json.Unmarshal(data, &info); err != nil {
			logrus.Warnf("解析容器設定檔 %s 失敗: %v", configPath, err)
			continue
		}
		containers = append(containers, &info)
	}

	return containers, nil
}
func findContainerInfo(identifier string) (*types.ContainerInfo, error) {
	files, err := os.ReadDir(config.ContainerStoragePath)
	if err != nil {
		return nil, fmt.Errorf("讀取容器儲存目錄失敗: %w", err)
	}

	for _, file := range files {
		if !file.IsDir() {
			continue
		}
		containerID := file.Name()
		configPath := filepath.Join(config.ContainerStoragePath, containerID, "config.json")

		data, err := os.ReadFile(configPath)
		if err != nil {
			logrus.Warnf("讀取 config.json 失敗 (%s): %v", configPath, err)
			continue
		}

		var info types.ContainerInfo
		if err := json.Unmarshal(data, &info); err != nil {
			logrus.Warnf("解析 config.json 失敗 (%s): %v", configPath, err)
			continue
		}

		// 比對完整 ID -> 名稱 -> ID 前綴
		if info.ID == identifier || info.Name == identifier || (len(identifier) <= len(info.ID) && info.ID[:len(identifier)] == identifier) {
			return &info, nil
		}
	}

	return nil, fmt.Errorf("找不到容器: %s", identifier)
}
func (m *Manager) Start(identifier string, opts *StartOptions) error {
	// 1. 查找容器並獲取其資訊
	info, err := findContainerInfo(identifier)
	if err != nil {
		return err
	}

	// 2. 檢查容器狀態
	if info.Status == types.Running {
		return fmt.Errorf("容器 %s 已經在運行中", identifier)
	}
	if info.Status != types.Stopped && info.Status != types.Created {
		return fmt.Errorf("無法啟動狀態為 %s 的容器", info.Status)
	}

	// 3. 準備重新啟動子行程
	log := logrus.WithField("containerID", info.ID)

	// 準備pipe
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("建立管道失敗: %w", err)
	}
	defer writePipe.Close()

	// 準備command
	selfPath, _ := os.Executable()
	cmd := exec.Command(selfPath, "init")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWNET,
	}
	cmd.ExtraFiles = []*os.File{readPipe}

	// 4. 啟動子行程
	attach := opts != nil && opts.Attach
	useTTY := attach && opts.Tty

	if attach && opts.Conn == nil {
		readPipe.Close()
		return fmt.Errorf("attach needs a valid connection")
	}

	var (
		ptmx    *os.File
		ttyDone chan struct{}
	)

	if useTTY {
		ptmx, err = pty.Start(cmd)
		if err != nil {
			readPipe.Close()
			return fmt.Errorf("cannot start subprocess (TTY mode): %w", err)
		}

		ttyDone = make(chan struct{})
		go func() {
			_, _ = io.Copy(opts.Conn, ptmx)
			close(ttyDone)
		}()
		go func() {
			_, _ = io.Copy(ptmx, opts.Conn)
		}()
	} else {
		if attach {
			cmd.Stdin = opts.Conn
			cmd.Stdout = opts.Conn
			cmd.Stderr = opts.Conn
		} else {
			// gocker-daemon will call this, so detach from terminal
			// set stdin/stdout/stderr to nil
			cmd.Stdin = nil
			cmd.Stdout = nil
			cmd.Stderr = nil
		}

		if err := cmd.Start(); err != nil {
			readPipe.Close()
			return fmt.Errorf("error starting subprocess: %w", err)
		}
	}
	readPipe.Close()

	newPid := cmd.Process.Pid
	log.Infof("容器已重新啟動，新的 PID 為 %d", newPid)

	// 5. 設定網路
	peerName, err := network.SetupVeth(newPid)
	if err != nil {
		_ = cmd.Process.Kill()
		if ptmx != nil {
			_ = ptmx.Close()
		}
		return fmt.Errorf("設定容器網路失敗: %w", err)
	}

	desiredIP := info.IPAddress
	if info.RequestedIP != "" {
		desiredIP = info.RequestedIP
	}

	allocatedIP, err := network.AllocateContainerIP(info.ID, desiredIP)
	if err != nil {
		_ = cmd.Process.Kill()
		if ptmx != nil {
			_ = ptmx.Close()
		}
		return fmt.Errorf("cannot allocate container IP: %w", err)
	}

	releaseIP := true
	defer func() {
		if releaseIP {
			if err := network.ReleaseContainerIP(info.ID); err != nil {
				log.Warnf("釋放容器 IP 失敗: %v", err)
			}
			releaseIP = false
		}
	}()

	info.IPAddress = allocatedIP

	// 6. 同步地將設定資訊寫入 pipe
	imageName, imageTag := pkg.Parse(info.Image)
	req := &types.RunRequest{
		ImageName:        imageName,
		ImageTag:         imageTag,
		ContainerCommand: info.Command,
		MountPoint:       info.MountPoint,
		ContainerLimits:  info.Limits,
		VethPeerName:     peerName,
		RequestedIP:      info.RequestedIP,
		IPAddress:        allocatedIP,
		ContainerID:      info.ID,
	}

	encoder := json.NewEncoder(writePipe)
	if err := encoder.Encode(req); err != nil {
		_ = cmd.Process.Kill()
		if ptmx != nil {
			_ = ptmx.Close()
		}
		log.Errorf("向管道寫入配置失敗: %v", err)
		return fmt.Errorf("向子行程傳遞設定失敗: %w", err)
	}
	writePipe.Close()

	// 7. 更新 config.json 狀態
	info.PID = newPid
	info.Status = types.Running
	info.FinishedAt = time.Time{}
	containerDir := filepath.Join(m.StoragePath, info.ID)
	if err := pkg.WriteContainerInfo(containerDir, info); err != nil {
		log.Warnf("更新容器狀態為 Running 失敗: %v", err)
	}

	// 8. 設定資源限制
	cgroupPath, err := m.SetupCgroup(info.Limits, newPid, info.ID)
	if err != nil {
		_ = cmd.Process.Kill()
		if ptmx != nil {
			_ = ptmx.Close()
		}
		return fmt.Errorf("設定 cgroup 失敗: %w", err)
	}

	// 9. 等待容器結束
	_ = cmd.Wait()

	if ptmx != nil {
		_ = ptmx.Close()
		if ttyDone != nil {
			<-ttyDone
		}
	}

	// 10. 容器再次停止後，更新最終狀態
	log.Info("容器已退出")
	info.PID = 0
	info.Status = types.Stopped
	info.FinishedAt = time.Now()
	_ = pkg.WriteContainerInfo(containerDir, info)

	// 11. 清理資源
	_ = m.CleanupCgroup(cgroupPath)

	return nil
}

func (m *Manager) GetInfo(identifier string) (*types.ContainerInfo, error) {
	return findContainerInfo(identifier)
}
