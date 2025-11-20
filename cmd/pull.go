// cmd/pull.go
package cmd

import (
	"encoding/json"
	"gocker/internal/api"
	"gocker/internal/types"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var pullCommand = &cobra.Command{
	Use:   "pull [IMAGE_NAME]",
	Short: "Pull an image from a remote repository",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		imageName := args[0]
		pullReq := types.PullRequest{
			Image: imageName,
		}
		payload, err := json.Marshal(pullReq)
		if err != nil {
			logrus.Fatalf("序列化 pull 請求失敗: %v", err)
		}
		req := types.Request{
			Command: "pull",
			Payload: payload,
		}
		res, err := api.SendRequest(req)
		if err != nil {
			logrus.Fatalf("與 gocker-daemon 通訊失敗: %v", err)
		}
		if res.Status != "success" {
			logrus.Fatalf("來自 Daemon 的錯誤: %s", res.Message)
		}

		logrus.Infof("成功拉取映像: %s", imageName)
	},
}

func init() {
	rootCmd.AddCommand(pullCommand)
}
