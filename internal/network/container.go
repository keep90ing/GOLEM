// internal/network/container.go
package network

import (
	"fmt"
	"net"

	"gocker/internal/config"

	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

// ConfigureContainerNetwork 設定容器內的網路
func ConfigureContainerNetwork(peerName, ipAddress string) error {
	// 1. 找到容器內的 veth peer
	peer, err := netlink.LinkByName(peerName)
	if err != nil {
		return fmt.Errorf("在容器內找不到 veth peer '%s': %v", peerName, err)
	}

	if ipAddress == "" {
		return fmt.Errorf("no IP address provided for container")
	}

	// 2. 將 veth peer 重新命名為 eth0
	if err := netlink.LinkSetName(peer, "eth0"); err != nil {
		return fmt.Errorf("重新命名 veth peer 為 eth0 失敗: %v", err)
	}

	// 3. 為 eth0 設定 IP 位址
	_, subnet, err := net.ParseCIDR(config.NetworkCIDR)
	if err != nil {
		return fmt.Errorf("cannot parse network CIDR '%s': %v", config.NetworkCIDR, err)
	}

	maskSize, _ := subnet.Mask.Size()
	addr, err := netlink.ParseAddr(fmt.Sprintf("%s/%d", ipAddress, maskSize))
	if err != nil {
		return fmt.Errorf("解析容器 IP 位址 '%s' 失敗: %v", ipAddress, err)
	}
	if err := netlink.AddrAdd(peer, addr); err != nil {
		return fmt.Errorf("為 eth0 設定 IP 失敗: %v", err)
	}

	// 4. 啟動 eth0 網卡
	if err := netlink.LinkSetUp(peer); err != nil {
		return fmt.Errorf("啟動 eth0 失敗: %v", err)
	}

	// 5. 設定預設路由，將所有流量指向 Bridge 的 IP
	gatewayIP := net.ParseIP(config.GatewayIP)
	if gatewayIP == nil {
		return fmt.Errorf("解析閘道 IP '%s' 失敗", config.GatewayIP)
	}
	route := &netlink.Route{
		Scope: netlink.SCOPE_UNIVERSE,
		Gw:    gatewayIP,
	}
	if err := netlink.RouteAdd(route); err != nil {
		return fmt.Errorf("設定預設路由失敗: %v", err)
	}

	// 啟動 lo 本地介面
	lo, _ := netlink.LinkByName("lo")
	_ = netlink.LinkSetUp(lo)

	logrus.Infof("容器內網路設定完成，IP: %s/%d", ipAddress, maskSize)
	return nil
}
