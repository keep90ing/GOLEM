// cmd/stop.go
package cmd

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"gocker/internal/container"
)

var stopAll bool

var stopCommand = &cobra.Command{
	Use:   "stop CONTAINER",
	Short: "Stop a running container",
	Long:  "Stop a running container by sending a termination signal.",
	Run: func(cmd *cobra.Command, args []string) {
		manager := container.NewManager()

		if stopAll {
			logrus.Info("Stopping all running containers")
			if len(args) > 0 {
				logrus.Fatal("The --all flag cannot be used with container IDs or names")
			}
			if err := manager.StopAllContainers(); err != nil {
				logrus.Fatalf("Failed to stop all containers: %v", err)
			}
			return
		} else {
			if len(args) != 1 {
				logrus.Fatal("Please provide exactly one container ID or name")
			}
			containerID := args[0]
			logrus.Infof("Stopping container: %s", containerID)

			if err := manager.StopContainer(containerID); err != nil {
				logrus.Fatalf("Failed to stop container: %v", err)
			}
		}
	},
}

func init() {
	stopCommand.Flags().BoolVarP(&stopAll, "all", "a", false, "Stop all running containers")
	rootCmd.AddCommand(stopCommand)
}
