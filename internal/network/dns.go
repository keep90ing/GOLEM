// internal/network/dns.go
package network

import (
	"fmt"
	"os"

	"gocker/internal/config"

	"github.com/sirupsen/logrus"
)

// SetupDNS 設定容器的 DNS
func SetupDNS() error {
	// 寫入 /etc/resolv.conf
	if err := os.WriteFile("/etc/resolv.conf", []byte(config.DNSServers), 0644); err != nil {
		return fmt.Errorf("寫入 /etc/resolv.conf 失敗: %w", err)
	}

	logrus.Info("DNS 設定完成")
	return nil
}
