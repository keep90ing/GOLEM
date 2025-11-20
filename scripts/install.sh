#!/bin/bash
set -e

# --- Configuration ---
INSTALL_PATH="/usr/local/bin"
STORAGE_PATH="/var/lib/gocker"
SERVICE_FILE="gocker-daemon.service"
SCRIPTS_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
PROJECT_ROOT="$(dirname "$SCRIPTS_DIR")"
SERVICE_FILE_PATH="$SCRIPTS_DIR/$SERVICE_FILE"

CLI_BIN="gocker"
DAEMON_BIN="gocker-daemon"

# --- Check for root privileges ---
if [ "$(id -u)" -ne 0 ]; then
    echo "Error: This installation script must be run as root. Please use sudo."
    exit 1
fi

# --- 1. Stopping Current service ---
echo "--> Stopping existing gocker-daemon service (if running)..."
systemctl stop $SERVICE_FILE || true

# --- 2. 複製執行檔 ---
install -m 0755 "$PROJECT_ROOT/$CLI_BIN" "$INSTALL_PATH/"
install -m 0755 "$PROJECT_ROOT/$DAEMON_BIN" "$INSTALL_PATH/"

# --- 3. 建立儲存目錄 ---
echo "--> Creating storage directory at $STORAGE_PATH..."
mkdir -p "$STORAGE_PATH/containers"

# --- 4. 安裝 systemd 服務檔案 ---
if [ ! -f "$SERVICE_FILE_PATH" ]; then
    echo "Error: Service file $SERVICE_FILE_PATH not found."
    exit 1
fi
echo "--> Installing systemd service file..."
cp "$SERVICE_FILE_PATH" /etc/systemd/system/

# --- 5. 重載、啟用並啟動服務 ---
echo "--> Enabling and starting gocker-daemon service..."
systemctl daemon-reload
systemctl enable $SERVICE_FILE
systemctl start $SERVICE_FILE

# --- 6. 驗證服務狀態 ---
sleep 2
if systemctl is-active --quiet $SERVICE_FILE; then
    echo "Gocker Daemon is running successfully."
else
    echo "Error: Gocker Daemon failed to start. Please check the service status with 'systemctl status $SERVICE_FILE'."
    exit 1
fi

echo " Gocker installed successfully!"