// internal/daemon/handlers.go
package daemon

import (
	"encoding/json"
	"fmt"
	"gocker/internal/container"
	"gocker/internal/types"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"
)

// handleConnection 這個函式是請求的分派中心
func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	log.Println("=======================================")
	log.Println("收到新的客戶端連線")
	log.Println("=======================================")

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var req types.Request
		if err := decoder.Decode(&req); err != nil {
			// 客戶端已正常關閉連線
			if err != io.EOF {
				log.Printf("解碼請求失敗: %v", err)
			}
			return
		}
		if req.Command == "exec" {
			// handleExec 會接管整個連線
			s.handleExec(req.Payload, conn)
			return
		}
		var res types.Response
		// 根據請求的 Command 欄位，分派到不同的處理函式
		switch req.Command {
		case "run":
			res = s.handleRun(req.Payload)
		case "ps":
			res = s.handlePs()
		case "start":
			var handled bool
			res, handled = s.handleStart(req.Payload, conn)
			if handled {
				return
			}
		case "images":
			res = s.handleImages()
		case "pull":
			res = s.handlePull(req.Payload)
		default:
			res = types.Response{Status: "error", Message: "未知的命令: " + req.Command}
		}

		if err := encoder.Encode(res); err != nil {
			log.Printf("編碼回應失敗: %v", err)
		}
	}
}

// handleRun 負責處理 "run" 命令
func (s *Server) handleRun(payload json.RawMessage) types.Response {
	var runReq types.RunRequest
	if err := json.Unmarshal(payload, &runReq); err != nil {
		return types.Response{Status: "error", Message: "解析 run 請求的 payload 失敗: " + err.Error()}
	}

	// 將具體工作「委派」給 ContainerManager
	containerID, err := s.ContainerManager.CreateAndRun(&runReq)
	if err != nil {
		return types.Response{Status: "error", Message: err.Error()}
	}

	return types.Response{Status: "success", Message: "容器 " + runReq.ContainerName + " 已成功啟動，ID: " + containerID}
}

// handlePs 負責處理 "ps" 命令
func (s *Server) handlePs() types.Response {
	containers, err := s.ContainerManager.List()
	if err != nil {
		return types.Response{Status: "error", Message: "獲取容器列表失敗: " + err.Error()}
	}

	data, err := json.Marshal(containers)
	if err != nil {
		return types.Response{Status: "error", Message: "序列化容器列表失敗: " + err.Error()}
	}

	return types.Response{Status: "success", Data: data}
}

// handleExec 負責處理 "exec" 命令
func (s *Server) handleExec(payload json.RawMessage, conn net.Conn) {
	var execReq types.ExecRequest
	if err := json.Unmarshal(payload, &execReq); err != nil {
		log.Printf("解析 exec 請求失敗: %v", err)
		_ = conn.Close()
		return
	}

	info, err := s.ContainerManager.GetInfo(execReq.ContainerID)
	if err != nil || info.Status != types.Running || info.PID == 0 {
		log.Printf("執行 exec 失敗: 找不到或容器 %s 不在運行狀態", execReq.ContainerID)
		_ = conn.Close()
		return
	}

	// 檢查該 PID 是否仍存在
	pidPath := fmt.Sprintf("/proc/%d", info.PID)
	if _, err := os.Stat(pidPath); os.IsNotExist(err) {
		log.Printf("容器 %s 的主行程 (PID %d) 已不存在，無法進入 namespace", execReq.ContainerID, info.PID)
		_ = conn.Close()
		return
	}

	pidStr := fmt.Sprintf("%d", info.PID)
	log.Printf("準備在容器 %s (PID: %s) 中執行命令 (TTY: %v)...", info.Name, pidStr, execReq.Tty)

	// 準備 nsenter 參數
	nsenterArgs := []string{"--preserve-credentials", "-t", pidStr, "-m", "-u", "-n", "-i", "-p", "--"}
	fullCommand := append(nsenterArgs, execReq.Command...)
	cmd := exec.Command("nsenter", fullCommand...)

	// 設定為非阻塞式
	defer func() {
		go func() {
			time.Sleep(500 * time.Millisecond)
			_ = conn.Close()
		}()
	}()

	// --- TTY 模式 ---
	if execReq.Tty {
		ptmx, err := pty.Start(cmd)
		if err != nil {
			log.Printf("⚠️ 在 PTY 模式啟動命令失敗，fallback 至非 TTY 模式: %v", err)
			execReq.Tty = false // ⬅ 自動降級
		} else {
			defer func() { _ = ptmx.Close() }()

			done := make(chan struct{})
			go func() {
				_, _ = io.Copy(conn, ptmx)
				close(done)
			}()
			_, _ = io.Copy(ptmx, conn)
			_ = cmd.Wait()
			<-done
			log.Printf("容器 %s (TTY) exec session 已結束", execReq.ContainerID)
			return
		}
	}

	// --- 非 TTY 模式 ---
	cmd.Stdin = conn
	cmd.Stdout = conn
	cmd.Stderr = conn

	if err := cmd.Start(); err != nil {
		log.Printf("非 TTY 模式啟動命令失敗: %v", err)
		_ = conn.Close()
		return
	}

	// 防止連線提早關閉
	go func() {
		_ = cmd.Wait()
		log.Printf("容器 %s (非 TTY) exec session 已結束", execReq.ContainerID)
		_ = conn.Close()
	}()
}

// handleStart 負責處理 "start" 命令
func (s *Server) handleStart(payload json.RawMessage, conn net.Conn) (types.Response, bool) {
	var startReq types.StartRequest

	if err := json.Unmarshal(payload, &startReq); err != nil {
		return types.Response{Status: "error", Message: "解析 start 請求的 payload 失敗: " + err.Error()}, false
	}

	if !startReq.Attach {
		if err := s.ContainerManager.Start(startReq.ContainerID, nil); err != nil {
			return types.Response{Status: "error", Message: err.Error()}, false
		}
		return types.Response{Status: "success", Message: "container has been started successfully: " + startReq.ContainerID}, false
	}

	opts := &container.StartOptions{
		Attach: true,
		Tty:    startReq.Tty,
		Conn:   conn,
	}

	if err := s.ContainerManager.Start(startReq.ContainerID, opts); err != nil {
		log.Printf("cannot start container %s and attach terminal: %v", startReq.ContainerID, err)
		_, _ = fmt.Fprintf(conn, "failed to start container: %v\n", err)
	}

	return types.Response{}, true
}

// handleImages 負責處理 "images" 命令
func (s *Server) handleImages() types.Response {
	images, err := s.ImageManager.ListImages()
	if err != nil {
		return types.Response{Status: "error", Message: "獲取映像檔列表失敗: " + err.Error()}
	}

	data, err := json.Marshal(images)
	if err != nil {
		return types.Response{Status: "error", Message: "序列化映像檔列表失敗: " + err.Error()}
	}

	return types.Response{Status: "success", Data: data}
}

// handlePull 負責處理 "pull" 命令
func (s *Server) handlePull(payload json.RawMessage) types.Response {
	var imageName types.PullRequest
	if err := json.Unmarshal(payload, &imageName); err != nil {
		return types.Response{Status: "error", Message: "解析 pull 請求的 payload 失敗: " + err.Error()}
	}

	if err := s.ImageManager.PullImage(imageName.Image); err != nil {
		return types.Response{Status: "error", Message: "拉取映像失敗: " + err.Error()}
	}
	return types.Response{Status: "success", Message: "成功拉取映像: " + imageName.Image}
}
