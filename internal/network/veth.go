// internal/network/veth.go
package network

import (
	"fmt"

	"gocker/internal/config"

	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

// SetupContainerNetwork 為容器設定網路
func SetupContainerNetwork(childPid int) (string, error) {
	vethName := fmt.Sprintf("veth-%d", childPid)
	peerName := fmt.Sprintf("peer-%d", childPid)

	// 建立 veth pair
	if err := createVethPair(vethName, peerName); err != nil {
		return "", fmt.Errorf("建立 veth pair 失敗: %v", err)
	}

	// 連接主機端 veth 到Bridge
	if err := connectVethToBridge(vethName); err != nil {
		return "", fmt.Errorf("連接 veth 到Bridge失敗: %v", err)
	}

	// 將容器端 veth 移入容器的網路 namespace
	if err := moveVethToContainer(peerName, childPid); err != nil {
		return "", fmt.Errorf("移動 veth 到容器失敗: %v", err)
	}

	return peerName, nil
}

// createVethPair 建立 veth pair
func createVethPair(vethName, peerName string) error {
	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: vethName, MTU: 1500},
		PeerName:  peerName,
	}

	return netlink.LinkAdd(veth)
}

// connectVethToBridge 將主機端 veth 連接到Bridge
func connectVethToBridge(vethName string) error {
	// 獲取Bridge
	bridge, err := netlink.LinkByName(config.BridgeName)
	if err != nil {
		return fmt.Errorf("找不到Bridge: %v", err)
	}

	// 獲取主機端 veth
	hostVeth, err := netlink.LinkByName(vethName)
	if err != nil {
		return fmt.Errorf("找不到主機端 veth: %v", err)
	}

	// 將 veth 連接到Bridge
	if err := netlink.LinkSetMaster(hostVeth, bridge); err != nil {
		return fmt.Errorf("設定 veth master 失敗: %v", err)
	}

	// 啟動主機端 veth
	if err := netlink.LinkSetUp(hostVeth); err != nil {
		return fmt.Errorf("啟動主機端 veth 失敗: %v", err)
	}

	return nil
}

// moveVethToContainer 將容器端 veth 移入容器的網路 namespace
func moveVethToContainer(peerName string, childPid int) error {
	peer, err := netlink.LinkByName(peerName)
	if err != nil {
		return fmt.Errorf("找不到容器端 veth: %v", err)
	}

	return netlink.LinkSetNsPid(peer, childPid)
}

func SetupVeth(pid int) (string, error) {
	logrus.Infof("Setting up veth for container with PID %d", pid)
	peerName, err := SetupContainerNetwork(pid)
	if err != nil {
		return "", fmt.Errorf("failed to setup container network: %v", err)
	}
	logrus.Infof("Successfully set up veth for container with PID %d", pid)
	return peerName, nil
}
