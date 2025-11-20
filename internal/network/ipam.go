package network

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/sirupsen/logrus"

	"gocker/internal/config"
)

type ipAllocationState struct {
	ContainerToIP map[string]string `json:"containerToIP"`
}

var ipamMu sync.Mutex

// AllocateContainerIP tries to allocate an IP address for the given container ID.
// If requestedIP is provided, the allocator will attempt to use it first. When it
// cannot be used (already taken, outside of range, etc.), the allocator will pick
// the next available IP from the configured subnet.
func AllocateContainerIP(containerID, requestedIP string) (string, error) {
	ipamMu.Lock()
	defer ipamMu.Unlock()

	state, err := loadIPAllocationState()
	if err != nil {
		return "", err
	}

	if existing, ok := state.ContainerToIP[containerID]; ok {
		return existing, nil
	}

	_, subnet, err := net.ParseCIDR(config.NetworkCIDR)
	if err != nil {
		return "", fmt.Errorf("failed to parse network CIDR %s: %w", config.NetworkCIDR, err)
	}

	used := make(map[string]struct{}, len(state.ContainerToIP))
	for _, ip := range state.ContainerToIP {
		used[ip] = struct{}{}
	}

	reserved := buildReservedIPs(subnet)

	if requestedIP != "" {
		if ip := net.ParseIP(requestedIP); ip != nil {
			if subnet.Contains(ip) && !ip.Equal(subnet.IP) && !ip.Equal(broadcastIP(subnet)) {
				if _, isReserved := reserved[ip.String()]; !isReserved {
					if _, alreadyUsed := used[ip.String()]; !alreadyUsed {
						state.ContainerToIP[containerID] = ip.String()
						if err := saveIPAllocationState(state); err != nil {
							return "", err
						}
						logrus.WithFields(logrus.Fields{
							"containerID": containerID,
							"ip":          ip.String(),
						}).Debug("Allocated requested IP for container")
						return ip.String(), nil
					}
					logrus.WithFields(logrus.Fields{
						"containerID": containerID,
						"ip":          ip.String(),
					}).Debug("Requested IP already in use, falling back to automatic assignment")
				} else {
					logrus.WithFields(logrus.Fields{
						"containerID": containerID,
						"ip":          ip.String(),
					}).Debug("Requested IP is reserved, falling back to automatic assignment")
				}
			} else {
				logrus.WithFields(logrus.Fields{
					"containerID": containerID,
					"ip":          ip.String(),
				}).Debug("Requested IP outside usable range, falling back to automatic assignment")
			}
		} else {
			logrus.WithFields(logrus.Fields{
				"containerID": containerID,
				"ip":          requestedIP,
			}).Debug("Failed to parse requested IP, falling back to automatic assignment")
		}
	}

	for candidate := nextIP(subnet.IP); subnet.Contains(candidate); candidate = nextIP(candidate) {
		if candidate.Equal(broadcastIP(subnet)) {
			continue
		}
		if _, isReserved := reserved[candidate.String()]; isReserved {
			continue
		}
		if _, alreadyUsed := used[candidate.String()]; alreadyUsed {
			continue
		}

		state.ContainerToIP[containerID] = candidate.String()
		if err := saveIPAllocationState(state); err != nil {
			return "", err
		}

		logrus.WithFields(logrus.Fields{
			"containerID": containerID,
			"ip":          candidate.String(),
		}).Debug("Allocated automatic IP for container")

		return candidate.String(), nil
	}

	return "", fmt.Errorf("no available IP addresses in %s", config.NetworkCIDR)
}

// ReleaseContainerIP releases the IP associated with the container ID. It is safe
// to call even if the container does not currently hold an allocation.
func ReleaseContainerIP(containerID string) error {
	ipamMu.Lock()
	defer ipamMu.Unlock()

	state, err := loadIPAllocationState()
	if err != nil {
		return err
	}

	if _, exists := state.ContainerToIP[containerID]; !exists {
		return nil
	}

	delete(state.ContainerToIP, containerID)
	return saveIPAllocationState(state)
}

func loadIPAllocationState() (*ipAllocationState, error) {
	if err := os.MkdirAll(config.NetworkStateDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to ensure network state directory: %w", err)
	}

	state := &ipAllocationState{ContainerToIP: make(map[string]string)}

	data, err := os.ReadFile(config.NetworkAllocationFile)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return nil, fmt.Errorf("failed to read allocation file: %w", err)
	}

	if len(data) == 0 {
		return state, nil
	}

	if err := json.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("failed to parse allocation file: %w", err)
	}

	if state.ContainerToIP == nil {
		state.ContainerToIP = make(map[string]string)
	}

	return state, nil
}

func saveIPAllocationState(state *ipAllocationState) error {
	if err := os.MkdirAll(config.NetworkStateDir, 0755); err != nil {
		return fmt.Errorf("failed to ensure network state directory: %w", err)
	}

	tmpPath := config.NetworkAllocationFile + ".tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open temp allocation file: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "    ")
	if err := encoder.Encode(state); err != nil {
		return fmt.Errorf("failed to write allocation file: %w", err)
	}

	if err := file.Sync(); err != nil {
		return fmt.Errorf("failed to sync allocation file: %w", err)
	}

	if err := os.Rename(tmpPath, config.NetworkAllocationFile); err != nil {
		return fmt.Errorf("failed to replace allocation file: %w", err)
	}

	return nil
}

func buildReservedIPs(subnet *net.IPNet) map[string]struct{} {
	reserved := make(map[string]struct{})

	if ip := net.ParseIP(config.GatewayIP); ip != nil {
		reserved[ip.String()] = struct{}{}
	}

	if bridgeIP, _, err := net.ParseCIDR(config.BridgeIP); err == nil {
		reserved[bridgeIP.String()] = struct{}{}
	}

	reserved[subnet.IP.String()] = struct{}{}
	reserved[broadcastIP(subnet).String()] = struct{}{}

	return reserved
}

func nextIP(ip net.IP) net.IP {
	dup := append(net.IP(nil), ip...)
	for i := len(dup) - 1; i >= 0; i-- {
		dup[i]++
		if dup[i] != 0 {
			break
		}
	}
	return dup
}

func broadcastIP(subnet *net.IPNet) net.IP {
	broadcast := append(net.IP(nil), subnet.IP...)
	for i := range broadcast {
		broadcast[i] |= ^subnet.Mask[i]
	}
	return broadcast
}

// GetAllocatedIP returns the IP currently allocated to the container. It is used
// primarily for informational purposes.
func GetAllocatedIP(containerID string) (string, error) {
	ipamMu.Lock()
	defer ipamMu.Unlock()

	state, err := loadIPAllocationState()
	if err != nil {
		return "", err
	}

	ip, ok := state.ContainerToIP[containerID]
	if !ok {
		return "", nil
	}
	return ip, nil
}

// CleanupContainerNetwork releases all network allocations associated with the
// container ID. At the moment, it only frees the allocated IP address, but it
// provides a single entry point for future cleanup tasks.
func CleanupContainerNetwork(containerID string) error {
	if err := ReleaseContainerIP(containerID); err != nil {
		return fmt.Errorf("failed to release container IP: %w", err)
	}
	return nil
}
