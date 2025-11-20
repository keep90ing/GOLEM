package cmd

import (
	"gocker/internal/config"
	"gocker/internal/container"
	"gocker/internal/types"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var newLimit types.ContainerLimits = types.ContainerLimits{
	MemoryLimit: config.InvalidLimit,
	PidsLimit:   config.InvalidLimit,
	CPULimit:    config.InvalidLimit,
}
var adjustCommand = &cobra.Command{
	Use:   "adjust CONTAINER",
	Short: "Adjust the resources of a running container",
	Long:  `Adjust the CPU and memory limits of a running container.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		identifier := args[0]
		mgr := container.NewManager()
		if err := mgr.AdjustResourceLimits(identifier, newLimit); err != nil {
			logrus.Fatalf("Failed to adjust resources for container %s: %v", identifier, err)
		}
		logrus.Infof("Successfully adjusted resources for container %s", identifier)
	},
}

func init() {
	adjustCommand.Flags().IntVar(&newLimit.PidsLimit, "pids-limit", config.InvalidLimit, "Limit the number of container tasks")
	adjustCommand.Flags().IntVarP(&newLimit.MemoryLimit, "memory", "m", config.InvalidLimit, "Limit the memory")
	adjustCommand.Flags().IntVar(&newLimit.CPULimit, "cpus", config.InvalidLimit, "Limit the number of CPUs")

	rootCmd.AddCommand(adjustCommand)
}
