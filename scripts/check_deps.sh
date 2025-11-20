#!/bin/bash
#
# This script checks for required build dependencies for the Gocker project,
# specifically Go, Clang, libbpf, and bpftool. It can also attempt to install them.

set -e

# --- Configuration ---
WANTED_GO_VERSION="1.25.2"

# --- Helper Functions ---
version_ge() {
    [ "$(printf '%s\n' "$1" "$2" | sort -V | head -n1)" = "$2" ]
}

# --- Go Language Check & Installation ---
check_go() {
    echo "--- Checking Go language installation..."

    GO_CMD=""
    if [ -n "$GO_EXECUTABLE" ] && [ -x "$GO_EXECUTABLE" ]; then
        GO_CMD="$GO_EXECUTABLE"
    elif [ -x "/usr/local/go/bin/go" ]; then
        GO_CMD="/usr/local/go/bin/go"
    elif command -v go &> /dev/null; then
        GO_CMD=$(command -v go)
    fi

    if [ -z "$GO_CMD" ]; then
        echo "Go is not installed. Preparing to install Go ${WANTED_GO_VERSION}..."
        install_go
        return
    fi

    CURRENT_GO_VERSION=$($GO_CMD version | { read -r _ _ v _; echo "${v#go}"; })
    echo " Found Go version: ${CURRENT_GO_VERSION}"

    if [ "$CURRENT_GO_VERSION" != "$WANTED_GO_VERSION" ]; then
        echo "警告：已安裝的 Go 版本 (${CURRENT_GO_VERSION}) 與要求的版本 (${WANTED_GO_VERSION}) 不符。"
        read -p "是否要移除舊版並安裝 ${WANTED_GO_VERSION}？[y/N] " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            install_go
        else
            echo "安裝被取消。請手動安裝 Go ${WANTED_GO_VERSION}。"
            exit 1
        fi
    else
        echo " Go 版本符合要求。"
    fi
}

install_go() {
    ARCH=$(uname -m)
    case $ARCH in
        x86_64) GO_ARCH="amd64" ;;
        aarch64) GO_ARCH="arm64" ;;
        *)
            echo "錯誤：不支援的系統架構 '$ARCH'。請手動安裝 Go。"
            exit 1
            ;;
    esac

    GO_TARBALL="go${WANTED_GO_VERSION}.linux-${GO_ARCH}.tar.gz"
    DOWNLOAD_URL="https://golang.org/dl/${GO_TARBALL}"

    echo "--> 正在從 ${DOWNLOAD_URL} 下載 Go..."
    if ! curl -Lo "/tmp/${GO_TARBALL}" "${DOWNLOAD_URL}"; then
        echo "錯誤：下載 Go 失敗。請檢查您的網路連線。"
        exit 1
    fi

    echo "--> 需要 sudo 權限來安裝 Go 到 /usr/local..."
    if [ -d "/usr/local/go" ]; then
        echo "--> 正在移除舊的 /usr/local/go..."
        sudo rm -rf /usr/local/go
    fi

    echo "--> 正在解壓縮 Go 到 /usr/local..."
    sudo tar -C /usr/local -xzf "/tmp/${GO_TARBALL}"
    rm "/tmp/${GO_TARBALL}"

    echo " Go ${WANTED_GO_VERSION} 已成功安裝到 /usr/local/go"
    echo ""
    echo "!!! 重要提示 !!!"
    echo "請將 Go 的路徑加入到您的環境變數中。"
    echo "請將以下這行加入到您的 ~/.bashrc 或 ~/.zshrc 檔案末尾："
    echo "  export PATH=\$PATH:/usr/local/go/bin"
    echo "然後執行 'source ~/.bashrc' 或 'source ~/.zshrc'，或重新開啟一個終端。"
    export PATH=$PATH:/usr/local/go/bin
}

# --- Clang Check & Installation ---
check_clang() {
    echo "--- 正在檢查 Clang 編譯器..."
    if command -v clang &> /dev/null; then
        echo " Clang 已安裝。"
        clang --version | head -n 1
    else
        echo "Clang 未安裝。準備嘗試自動安裝..."
        install_package "clang"
        if ! command -v clang &> /dev/null; then
             echo "錯誤：Clang 安裝失敗。請手動安裝。" && exit 1
        fi
    fi
}

# --- LLVM Tools Check & Installation ---
check_llvm() {
    echo "--- 正在檢查 LLVM 工具 (llvm-strip)..."
    if command -v llvm-strip &> /dev/null; then
        echo " LLVM 工具已安裝。"
    else
        echo "LLVM 工具未安裝。準備嘗試自動安裝..."
        install_package "llvm"
        if ! command -v llvm-strip &> /dev/null; then
            echo "錯誤：LLVM 安裝失敗。請手動安裝。" && exit 1
        fi
    fi
}

# --- eBPF Dependencies Check & Installation ---
check_bpf_deps() {
    echo "--- 正在檢查 eBPF 開發依賴 (libbpf, bpftool)..."

    LIBBPF_HEADER="/usr/include/bpf/bpf_helpers.h"
    NEEDS_INSTALL=0

    if [ ! -f "$LIBBPF_HEADER" ]; then
        echo "警告：找不到 libbpf 標頭檔 ($LIBBPF_HEADER)。"
        NEEDS_INSTALL=1
    fi

    if ! command -v bpftool &> /dev/null; then
        echo "警告：找不到 bpftool 命令。"
        NEEDS_INSTALL=1
    fi

    if [ "$NEEDS_INSTALL" -eq 1 ]; then
        echo "準備嘗試自動安裝 eBPF 開發工具..."
        echo "--> 需要 sudo 權限來安裝..."
        install_package "bpf-tools"
    fi

    if [ -f "$LIBBPF_HEADER" ] && command -v bpftool &> /dev/null; then
        echo " libbpf 和 bpftool 已準備就緒。"
    else
        echo "錯誤：eBPF 開發依賴安裝失敗。請手動安裝。"
        exit 1
    fi
}

# --- Generic Package Installer Helper ---
install_package() {
    local package_name=$1

    if command -v apt-get &> /dev/null; then
        sudo apt-get update
        if [ "$package_name" = "clang" ]; then
            sudo apt-get install -y clang llvm
        elif [ "$package_name" = "llvm" ]; then
            sudo apt-get install -y llvm
        elif [ "$package_name" = "bpf-tools" ]; then
            sudo apt-get install -y libbpf-dev linux-tools-common linux-tools-$(uname -r)
        fi
    elif command -v dnf &> /dev/null; then
        if [ "$package_name" = "clang" ]; then
            sudo dnf install -y clang
        elif [ "$package_name" = "bpf-tools" ]; then
            sudo dnf install -y libbpf-devel bpftool kernel-devel
        fi
    elif command -v yum &> /dev/null; then
        echo "yum-based systems might require enabling EPEL or other repos. Manual installation is recommended."
        exit 1
    elif command -v pacman &> /dev/null; then
        if [ "$package_name" = "clang" ]; then
            sudo pacman -S --noconfirm clang
        elif [ "$package_name" = "llvm" ]; then
            sudo pacman -S --noconfirm llvm
        elif [ "$package_name" = "bpf-tools" ]; then
            sudo pacman -S --noconfirm libbpf bpftool linux-headers
        fi
    else
        echo "Error: No supported package manager found. Please install $package_name manually."
        exit 1
    fi
}


# --- Main Execution ---
check_go
check_clang
check_llvm
check_bpf_deps
echo "Installation of all dependencies is complete."
