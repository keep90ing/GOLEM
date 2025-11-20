// internal/container/rootfs.go
package container

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"gocker/internal/config"
	"gocker/internal/types"

	"github.com/sirupsen/logrus"
)

// SetupRootfs 準備容器的根檔案系統，包括掛載 OverlayFS 和執行 pivot_root
// 參數 mountPoint 是容器最終的掛載點路徑
func SetupRootfs(mountPoint string, imageName, imageTag string) error {
	log := logrus.WithFields(logrus.Fields{
		"image":      fmt.Sprintf("%s:%s", imageName, imageTag),
		"mountPoint": mountPoint,
	})
	log.Info("正在設定容器 rootfs...")

	// 1. 尋找基礎映像的 rootfs 路徑 (lowerdir)
	imageRootfsPath, err := findImageRootfsPath(imageName, imageTag)
	if err != nil {
		return fmt.Errorf("找不到基礎映像 '%s:%s': %w", imageName, imageTag, err)
	}
	log.Infof("找到基礎映像 rootfs (lowerdir): %s", imageRootfsPath)

	/*
	 * 	2. 建立 OverlayFS 所需的目錄
	 *  -  upperdir: 存放容器內檔案變更 (寫入層)
	 *  -  workdir: OverlayFS 的工作目錄
	 */
	containerBasePath := filepath.Dir(mountPoint)
	upperdir := filepath.Join(containerBasePath, "upper")
	workdir := filepath.Join(containerBasePath, "work")

	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(upperdir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(workdir, 0755); err != nil {
		return err
	}

	// 3. 組合 OverlayFS 的掛載選項
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", imageRootfsPath, upperdir, workdir)

	// 4. 掛載 OverlayFS
	log.Infof("正在掛載 OverlayFS, opts: %s", opts)
	if err := syscall.Mount("overlay", mountPoint, "overlay", 0, opts); err != nil {

		// if mount error is "Device or resource busy", check if the mount point is already mounted
		// and fstype is overlay
		if errors.Is(err, syscall.EBUSY) && checkFSType(mountPoint, "overlay") {
			log.Infof("掛載點 %s 已經掛載 OverlayFS，跳過掛載步驟", mountPoint)
		} else {
			return fmt.Errorf("掛載 OverlayFS 失敗: %w", err)
		}
	}

	// 5. 執行 pivot_root 將根目錄切換到 mountPoint
	if err := PivotRoot(mountPoint); err != nil {
		return fmt.Errorf("pivot_root 執行失敗: %w", err)
	}

	// 6. 在新的根目錄下掛載虛擬檔案系統
	log.Info("正在掛載 /proc, /sys, /dev...")
	if err := syscall.Mount("proc", "/proc", "proc", 0, ""); err != nil {
		return fmt.Errorf("掛載 /proc 失敗: %w", err)
	}
	if err := syscall.Mount("sysfs", "/sys", "sysfs", 0, ""); err != nil {
		return fmt.Errorf("掛載 /sys 失敗: %w", err)
	}

	// mount dev, devpts, tmpfs
	os.MkdirAll("/dev", 0755)
	syscall.Mount("tmpfs", "/dev", "tmpfs", 0, "mode=0755")
	os.MkdirAll("/dev/pts", 0755)
	os.MkdirAll("/dev/shm", 0755)
	os.MkdirAll("/tmp", 01777)
	os.MkdirAll("/run", 0755)
	syscall.Mount("devpts", "/dev/pts", "devpts", 0, "newinstance,ptmxmode=0666,mode=0620,gid=5")
	// create symlink for ptmx
	os.Remove("/dev/ptmx")
	os.Symlink("pts/ptmx", "/dev/ptmx")
	// mount /tmp, /run, /dev/shm as tmpfs
	syscall.Mount("tmpfs", "/tmp", "tmpfs", 0, "mode=1777")
	syscall.Mount("tmpfs", "/run", "tmpfs", 0, "mode=0755")
	syscall.Mount("tmpfs", "/dev/shm", "tmpfs", 0, "mode=1777")

	oldUmask := syscall.Umask(0)

	// create essential device nodes
	syscall.Mknod("/dev/null", syscall.S_IFCHR|0666, int((1<<8)|3)) // major=1, minor=3
	syscall.Mknod("/dev/zero", syscall.S_IFCHR|0666, int((1<<8)|5))
	syscall.Mknod("/dev/full", syscall.S_IFCHR|0666, int((1<<8)|7))
	syscall.Mknod("/dev/random", syscall.S_IFCHR|0666, int((1<<8)|8))
	syscall.Mknod("/dev/urandom", syscall.S_IFCHR|0666, int((1<<8)|9))
	syscall.Mknod("/dev/tty", syscall.S_IFCHR|0666, int((5<<8)|0))

	syscall.Umask(oldUmask)

	// set permissions for /tmp and /dev/shm (we've set it at 5.1, but just to be sure)
	os.Chmod("/tmp", 01777)
	os.Chmod("/dev/shm", 01777)

	return nil
}

// PivotRoot 切換根目錄
func PivotRoot(newRoot string) error {
	// 確保當前的 / 掛載傳播類型為 private，避免影響主機
	if err := syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""); err != nil {
		return fmt.Errorf("將根掛載設為 private 失敗: %w", err)
	}

	// 為了滿足 pivot_root 的要求，newRoot 必須是一個掛載點
	if err := syscall.Mount(newRoot, newRoot, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		return fmt.Errorf("綁定掛載 newRoot 失敗: %w", err)
	}

	// 建立 .old_root 目錄，用來臨時存放舊的根
	putold := filepath.Join(newRoot, ".old_root")
	if err := os.MkdirAll(putold, 0700); err != nil {
		return fmt.Errorf("建立 .old_root 失敗: %w", err)
	}

	// 執行 pivot_root
	if err := syscall.PivotRoot(newRoot, putold); err != nil {
		return fmt.Errorf("pivot_root 失敗: %w", err)
	}

	// 切換到新的根目錄
	if err := os.Chdir("/"); err != nil {
		return fmt.Errorf("chdir 到 / 失敗: %w", err)
	}

	// 卸載並移除舊的根目錄 (現在的路徑是相對於 newRoot)
	if err := syscall.Unmount("/.old_root", syscall.MNT_DETACH); err != nil {
		return fmt.Errorf("卸載 .old_root 失敗: %w", err)
	}
	if err := os.RemoveAll("/.old_root"); err != nil {
		logrus.Warnf("移除 .old_root 失敗: %v", err)
	}
	return nil
}

// findImageRootfsPath 透過 manifest 檔案尋找映像的 rootfs 路徑
func findImageRootfsPath(imageName, imageTag string) (string, error) {
	manifestPath := filepath.Join(config.ImagesDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", fmt.Errorf("讀取映像 manifest %s 失敗: %w", manifestPath, err)
	}

	var manifests []types.ImageManifest
	if err := json.Unmarshal(data, &manifests); err != nil {
		return "", fmt.Errorf("解析 manifest.json 失敗: %w", err)
	}

	searchName := fmt.Sprintf("%s:%s", imageName, imageTag)
	for _, m := range manifests {
		if m.RepoTag == searchName {
			return filepath.Join(config.ImagesDir, m.ImageID, "rootfs"), nil
		}
	}

	return "", fmt.Errorf("在 manifest 中找不到映像 '%s'", searchName)
}

/*
檢查mountPoint是否為指定的fstype

/proc/mounts 格式

* device mountPoint fstype options dump pass
*/
func checkFSType(mountPoint, fstype string) bool {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		logrus.Warnf("讀取 /proc/mounts 失敗: %v", err)
		return false
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[1] == mountPoint {
			if fields[2] != fstype {
				logrus.Warnf("掛載點 %s 的 fstype 為 %s, 不是預期的 %s", mountPoint, fields[2], fstype)
				return false
			}
			return true
		}
	}
	logrus.Warnf("找不到掛載點 %s", mountPoint)
	return false
}
