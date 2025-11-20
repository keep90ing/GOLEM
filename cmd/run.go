// cmd/run.go
package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"gocker/internal"
	"gocker/pkg"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"gocker/internal/config"
	"gocker/internal/types"
)

var request types.RunRequest
var initInstructionFile string

var runCommand = &cobra.Command{
	Use:   "run [OPTIONS] IMAGE COMMAND [ARG...]",
	Short: "Run a command in a new container",
	Long: `
Run a command in a new container with specified image and command.
Use double dashes (--) if you want to pass arguments to the command. like 'gocker run --<flags>... -- /bin/sh'."`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		imageName, imageTag := pkg.Parse(args[0])
		logrus.Infof("Image: %s, Tag: %s", imageName, imageTag)

		request.ImageName = imageName
		request.ImageTag = imageTag

		if len(args) == 1 {
			logrus.Info("沒有指定容器命令，預設使用 /bin/sh")
			request.ContainerCommand = config.DefaultCommand
			request.ContainerArgs = []string{}
		} else {
			request.ContainerCommand = args[1]
			if len(args) > 2 {
				request.ContainerArgs = args[2:]
			}
		}
		/*
		 * Initial file name is "Gockerfile" by default
		 * User can override it by --init-file flag
		 * If the specified file does not exist, we will skip the initialization step
		 * If the file is specified but does not exist, we will exit with error
		 * If the file is not specified and does not exist, we will skip the initialization step
		 * If the file exists, we will read the commands from the file and pass it to the container
		 * The commands will be executed in the container before the main command
		 */
		instructionPath := initInstructionFile
		if instructionPath != "" {
			logrus.Debugf("Initialization instructions file: %s", instructionPath)
			commands, err := loadInitCommands(instructionPath)
			if err == nil && len(commands) > 0 {
				logrus.Infof("Loaded %d initialization commands from: %s", len(commands), instructionPath)
				request.InitCommands = commands
			} else if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					logrus.Infof("Cannot find initialization instructions file %s, skipping initialization step", instructionPath)
				} else {
					logrus.Fatalf("Failed to read initialization instructions file: %v", err)
				}
			}
		}
		if err := internal.RunContainer(&request); err != nil {
			logrus.Fatalf("Failed to run container: %v", err)
		}
	},
}

func init() {
	runCommand.Flags().StringVarP(&request.ContainerName, "name", "", "", "Assign a name to the container")
	runCommand.Flags().IntVar(&request.PidsLimit, "pids-limit", config.DefaultPidsLimit, "Limit the number of container tasks")
	runCommand.Flags().IntVarP(&request.MemoryLimit, "memory", "m", config.DefaultMemoryLimit, "Limit the memory")
	runCommand.Flags().IntVar(&request.CPULimit, "cpus", config.DefaultCPULimit, "Limit the number of CPUs")
	runCommand.Flags().StringVar(&request.RequestedIP, "ip", "", "Request a specific IPv4 address for the container")
	runCommand.Flags().StringVar(&initInstructionFile, "init-file", "", fmt.Sprintf("Path to initialization instructions file (default %s)",
		config.DefaultInitInstructionFile))
	rootCmd.AddCommand(runCommand)
}

/*
* Here we load initialization commands from a specified file.
* Each line in the file represents a command to be executed inside the container before the main command.
* Lines starting with '#' are treated as comments and ignored.
* Empty lines are also ignored.
 */
func loadInitCommands(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to open initialization instructions file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var commands []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		commands = append(commands, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read initialization instructions file: %w", err)
	}

	return commands, nil
}
