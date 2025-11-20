// internal/network/bridge.go
package network

import (
	"fmt"
	"os/exec"
	"strings"

	"gocker/internal/config"

	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

// IPTablesRule iptables 規則結構
type IPTablesRule struct {
	Table  string
	Chain  string
	Action string
	Args   []string
}

// SetupBridge 設定Bridge
func SetupBridge() error {
	// 檢查Bridge是否已存在
	if bridge, err := netlink.LinkByName(config.BridgeName); err == nil {
		return ensureBridgeIP(bridge)
	}

	logrus.Infof("Bridge '%s' 不存在，開始建立...", config.BridgeName)
	// 建立新的Bridge
	if err := createBridge(); err != nil {
		return fmt.Errorf("建立Bridge失敗: %v", err)
	}

	// 設定 IP 位址
	if err := setBridgeIP(); err != nil {
		return fmt.Errorf("設定Bridge IP 失敗: %v", err)
	}

	// 啟動Bridge
	if err := enableBridge(); err != nil {
		return fmt.Errorf("啟動Bridge失敗: %v", err)
	}

	// 設定 iptables 規則
	if err := setupIPTablesRules(); err != nil {
		return fmt.Errorf("設定 iptables 規則失敗: %v", err)
	}

	return nil
}

// createBridge 建立Bridge
func createBridge() error {
	fmt.Printf("建立Bridge '%s'\n", config.BridgeName)

	bridge := &netlink.Bridge{
		LinkAttrs: netlink.LinkAttrs{
			Name: config.BridgeName,
		},
	}

	return netlink.LinkAdd(bridge)
}

// setBridgeIP 設定Bridge IP
func setBridgeIP() error {
	bridge, err := netlink.LinkByName(config.BridgeName)
	if err != nil {
		return err
	}

	addr, err := netlink.ParseAddr(config.BridgeIP)
	if err != nil {
		return err
	}

	return netlink.AddrAdd(bridge, addr)
}

// enableBridge 啟動Bridge
func enableBridge() error {
	bridge, err := netlink.LinkByName(config.BridgeName)
	if err != nil {
		return err
	}

	return netlink.LinkSetUp(bridge)
}

// ensureBridgeIP 確保Bridge有 IP 位址
func ensureBridgeIP(bridge netlink.Link) error {
	addrs, err := netlink.AddrList(bridge, netlink.FAMILY_V4)
	if err != nil {
		return err
	}

	if len(addrs) == 0 {
		addr, err := netlink.ParseAddr(config.BridgeIP)
		if err != nil {
			return err
		}
		return netlink.AddrAdd(bridge, addr)
	}

	return nil
}

// setupIPTablesRules 設定 iptables 規則
func setupIPTablesRules() error {
	rules := []IPTablesRule{
		// MASQUERADE 規則
		{
			Table:  "nat",
			Chain:  "POSTROUTING",
			Action: "MASQUERADE",
			Args:   []string{"-s", config.NetworkCIDR, "!", "-o", config.BridgeName},
		},
		// FORWARD 規則
		{
			Chain:  "FORWARD",
			Action: "ACCEPT",
			Args:   []string{"-i", config.BridgeName},
		},
		{
			Chain:  "FORWARD",
			Action: "ACCEPT",
			Args:   []string{"-o", config.BridgeName},
		},
	}

	for _, rule := range rules {
		if err := rule.Apply(); err != nil {
			fmt.Printf("警告: 設定 iptables 規則失敗: %v\n", err)
		}
	}

	return nil
}

// Apply 應用 iptables 規則
func (r *IPTablesRule) Apply() error {
	// 檢查規則是否已存在
	if r.exists() {
		return nil
	}

	// 建立 iptables 命令
	args := []string{"iptables"}

	if r.Table != "" {
		args = append(args, "-t", r.Table)
	}

	args = append(args, "-A", r.Chain)
	args = append(args, r.Args...)
	args = append(args, "-j", r.Action)

	cmd := exec.Command(args[0], args[1:]...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("執行 %s 失敗: %v", strings.Join(args, " "), err)
	}

	fmt.Printf("已新增 iptables 規則: %s\n", strings.Join(args[1:], " "))
	return nil
}

// exists 檢查規則是否已存在
func (r *IPTablesRule) exists() bool {
	args := []string{"iptables"}

	if r.Table != "" {
		args = append(args, "-t", r.Table)
	}

	args = append(args, "-C", r.Chain)
	args = append(args, r.Args...)
	args = append(args, "-j", r.Action)

	cmd := exec.Command(args[0], args[1:]...)
	return cmd.Run() == nil
}
