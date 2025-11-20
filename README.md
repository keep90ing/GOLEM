# Installation
We provide a Makefile to automate dependency checking, compilation, and installation.
1. Clone the repository
```bash
git clone [https://github.com/ArBin1020/Gocker.git](https://github.com/ArBin1020/Gocker.git)
cd gocker
```
2. Build and Install
Run the following command to check dependencies, build the binaries, and install them to your system path:
```bash
make check-deps && make && sudo make install
```
# Quick Start
1. Explore Commands
First, check the available commands and flags using the help option:
```bash
gocker --help
```
<details> <summary>Click to see output</summary>

```Plaintext
Gocker is a simple container runtime written in Go.

Usage:
  gocker [command]

Available Commands:
  adjust      Adjust the resources of a running container
  completion  Generate the autocompletion script for the specified shell
  exec        Execute commands within a running container
  help        Help about any command
  images      List all locally stored images
  ps          List all containers
  pull        Pull an image from a remote repository
  rm          Remove container by ID or NAME.
  run         Run a command in a new container
  start       Restart a stopped container
  stop        Stop a running container

Flags:
  -h, --help               help for gocker
  -l, --log-level string   Set the logging level ("trace"|"debug"|"info"|"warn"|"error"|"fatal"|"panic") (default "debug")

Use "gocker [command] --help" for more information about a command.
```
</details>

2. Pull an Image
Download a lightweight image (e.g., Alpine Linux) from a remote repository.
```bash
sudo gocker pull alpine:latest
```
3. Run a Container
Start a new container in interactive mode (-it). You will be dropped into a shell inside the isolated environment.
```bash
sudo gocker run -it alpine /bin/sh
```

# Uninstall
```bash
sudo make uninstall
```


