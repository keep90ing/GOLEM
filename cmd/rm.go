// gocker/cmd/rm.go
package cmd

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"gocker/internal"
	"gocker/pkg"
)

var removeAll bool

var rmCommand = &cobra.Command{
	Use:   "rm [CONTAINER_ID|CONTAINER_NAME]",
	Short: "Remove container by ID or NAME.",
	Run: func(cmd *cobra.Command, args []string) {
		remover := internal.NewRemover()

		if removeAll {
			logrus.Info("Removing all containers")
			if len(args) > 0 {
				logrus.Fatal("The --all flag cannot be used with container IDs or names")
			}
			if err := remover.RemoveAllContainers(); err != nil {
				logrus.Fatalf("Failed to remove all containers: %v", err)
			}
			return
		}
		if len(args) != 1 {
			logrus.Fatal("Please provide exactly one container ID or name")
		}

		identifier := args[0]
		container, err := pkg.ResolveContainer(identifier)
		if err != nil {
			logrus.Fatalf("%v", err)
		}

		if err := remover.RemoveContainer(container.ID); err != nil {
			logrus.Fatalf("Failed to remove container %s: %v", identifier, err)
		}
		fmt.Println(container.ID)
	},
}

func init() {
	rmCommand.Flags().BoolVarP(&removeAll, "all", "a", false, "Remove all containers")
	rootCmd.AddCommand(rmCommand)
}
