package container

import (
	"encoding/json"
	"fmt"
	"gocker/internal/network"
	"gocker/internal/types"
	"os"
	"os/exec"
	"syscall"

	"github.com/sirupsen/logrus"
)

// InitContainer 負責所有子行程在新的 Namespace 中的初始化工作
func InitContainer() error {

	log := logrus.WithFields(logrus.Fields{
		"pid": os.Getpid(),
	})
	log.Info("子行程: 在新的 Namespace 中啟動...")

	//  從傳入的管道 (fd 3) 中讀取並解析 RunRequest 配置
	pipe := os.NewFile(uintptr(3), "pipe")
	defer pipe.Close()

	var req types.RunRequest
	decoder := json.NewDecoder(pipe)
	if err := decoder.Decode(&req); err != nil {
		return fmt.Errorf("子行程: 從管道讀取配置失敗: %w", err)
	}
	log.Infof("子行程: 成功解析配置，準備執行命令 '%s'", req.ContainerCommand)

	//  設定容器的主機名稱
	if err := syscall.Sethostname([]byte(req.ContainerName)); err != nil {
		return fmt.Errorf("子行程: 設定主機名稱失敗: %w", err)
	}

	//  設定根檔案系統 (Rootfs)
	if err := SetupRootfs(req.MountPoint, req.ImageName, req.ImageTag); err != nil {
		return fmt.Errorf("子行程: 設定 rootfs 失敗: %w", err)
	}
	log.Info("子行程: Rootfs 掛載成功")

	//  設定 DNS
	if err := network.SetupDNS(); err != nil {
		return fmt.Errorf("子行程: 設定 DNS 失敗: %w", err)
	}

	//  在容器內部設定網路
	if err := network.ConfigureContainerNetwork(req.VethPeerName, req.IPAddress); err != nil {
		return fmt.Errorf("子行程: 設定容器網路失敗: %w", err)
	}
	log.Info("子行程: 容器內網路設定完成")

	//  Run initialization commands if any
	log.Infof("Subprocess: %d initialization commands to run", len(req.InitCommands))
	if len(req.InitCommands) > 0 {
		log.Infof("Subprocess %d initialization commands", len(req.InitCommands))
		for idx, commandLine := range req.InitCommands {
			log.Infof("Subprocess: (%d/%d) Executing: %s", idx+1, len(req.InitCommands), commandLine)
			initCmd := exec.Command("/bin/sh", "-c", commandLine)
			initCmd.Stdout = os.Stdout
			initCmd.Stderr = os.Stderr
			initCmd.Stdin = os.Stdin
			if err := initCmd.Run(); err != nil {
				return fmt.Errorf("subprocess: initialization command '%s' failed: %w", commandLine, err)
			}
		}
	}

	//  使用 syscall.Exec 執行使用者指定的命令
	cmdPath, err := exec.LookPath(req.ContainerCommand)
	if err != nil {
		return fmt.Errorf("子行程: 找不到命令 '%s': %w", req.ContainerCommand, err)
	}

	//  執行使用者命令
	log.Infof("子行程: 執行 exec syscall: %s", cmdPath)
	args := append([]string{req.ContainerCommand}, req.ContainerArgs...)
	if err := syscall.Exec(cmdPath, args, os.Environ()); err != nil {
		return fmt.Errorf("子行程: exec 失敗: %w", err)
	}

	return nil
}
