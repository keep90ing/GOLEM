// internal/config/constants.go
package config

const (
	// 儲存路徑
	GockerStorage        = "/var/lib/gocker"
	ImagesDir            = GockerStorage + "/images"
	ContainersDir        = GockerStorage + "/containers"
	ContainerStoragePath = "/var/lib/gocker/containers"
	ManifestPath         = ImagesDir + "/manifest.json"

	// 網路設定
	BridgeName            = "gocker0"
	BridgeIP              = "10.20.0.1/24"
	NetworkCIDR           = "10.20.0.0/24"
	ContainerIP           = "10.20.0.2/24"
	GatewayIP             = "10.20.0.1"
	NetworkStateDir       = GockerStorage + "/network"
	NetworkAllocationFile = NetworkStateDir + "/allocations.json"

	// DNS 設定
	DNSServers = `nameserver 8.8.8.8
	              nameserver 1.1.1.1
	              nameserver 8.8.4.4
				 `

	// Cgroup 設定
	CgroupRoot = "/sys/fs/cgroup"
	CgroupName = "gocker"

	// 資源限制
	DefaultCPULimit    = 1                 // 1 core CPU
	DefaultMemoryLimit = 200 * 1024 * 1024 // 100MB Memory
	DefaultPidsLimit   = 100               // 100 processes
	InvalidLimit       = -1

	// 日誌設定
	DefaultLogLevel = "debug"

	// 執行檔相關
	DefaultCommand = "/bin/bash"

	// Initialization instructions file
	DefaultInitInstructionFile = "Gockerfile"

	SocketPath = "/var/run/gocker.sock"
)
