package types

import (
	"encoding/json"
	"time"
)

type RunRequest struct {
	ImageName        string
	ImageTag         string
	ContainerName    string
	ContainerCommand string
	ContainerID      string
	ContainerArgs    []string
	MountPoint       string
	VethPeerName     string
	InitCommands     []string
	RequestedIP      string
	IPAddress        string
	ContainerLimits
}

type ContainerLimits struct {
	MemoryLimit int
	PidsLimit   int
	CPULimit    int
}

// ContainerStatus 容器的狀態
const (
	Running = "running"
	Stopped = "stopped"
	Created = "created"
)

// ContainerInfo 用於儲存容器的metadata
type ContainerInfo struct {
	ID          string          `json:"id"`
	PID         int             `json:"pid"`
	Name        string          `json:"name"`
	Command     string          `json:"command"`
	Status      string          `json:"status"`
	CreatedAt   time.Time       `json:"createdAt"`
	Image       string          `json:"image"`
	MountPoint  string          `json:"mountPoint"`
	RequestedIP string          `json:"requestedIP,omitempty"`
	IPAddress   string          `json:"ipAddress,omitempty"`
	FinishedAt  time.Time       `json:"finishedAt,omitempty"`
	Limits      ContainerLimits `json:"limits,omitempty"`
}

// ImageManifest Image 的結構
type ImageManifest struct {
	ImageID string `json:"imageID"` // 映像的唯一 ID
	RepoTag string `json:"repoTag"` // 映像的標籤
}

// Request 是 CLI 向 Daemon 發送的通用結構
type Request struct {
	Command string          `json:"command"`           // 例如 "ps", "run", "stop"
	Payload json.RawMessage `json:"payload,omitempty"` // 承載具體命令的數據
}

// Response 是 Daemon 向 CLI 回應的通用結構
type Response struct {
	Status  string          `json:"status"`            // "success" 或 "error"
	Message string          `json:"message,omitempty"` // 簡單的文字訊息或錯誤資訊
	Data    json.RawMessage `json:"data,omitempty"`    // 承載複雜的數據
}

// ExecRequest 用於執行命令的請求結構
type ExecRequest struct {
	ContainerID string   `json:"container_id"` // 容器 ID
	Command     []string `json:"command"`      // 要執行的命令及其參數
	Tty         bool     `json:"tty"`          // 是否分配 TTY
}

type StartRequest struct {
	ContainerID string `json:"container_id"`
	Attach      bool   `json:"attach"`
	Tty         bool   `json:"tty"`
}

type PullRequest struct {
	Image string `json:"image"` // 例如 "alpine:latest"
}
