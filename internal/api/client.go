// internal/api/client.go
package api

import (
	"encoding/json"
	"fmt"
	"net"

	"gocker/internal/config"
	"gocker/internal/types"
)

func SendRequest(req types.Request) (*types.Response, error) {
	conn, err := net.Dial("unix", config.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("無法連接到 gocker-daemon: %w", err)
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("發送請求失敗: %w", err)
	}

	var res types.Response
	if err := json.NewDecoder(conn).Decode(&res); err != nil {
		return nil, fmt.Errorf("讀取回應失敗: %w", err)
	}

	return &res, nil
}
