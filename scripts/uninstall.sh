#!/bin/bash
set -e

SERVICE_FILE="gocker-daemon.service"
INSTALL_PATH="/usr/local/bin"
BINS=("gocker" "gocker-daemon" "sched_monitor")
STORAGE_PATH="/var/lib/gocker"

if [ "$(id -u)" -ne 0 ]; then
    echo "Error: This uninstallation script must be run as root."
    exit 1
fi

echo "--- Removing Gocker system ---"

systemctl stop $SERVICE_FILE || true
systemctl disable $SERVICE_FILE || true
rm -f "/etc/systemd/system/$SERVICE_FILE"
systemctl daemon-reload

for bin in "${BINS[@]}"; do
    rm -f "$INSTALL_PATH/$bin"
done

echo "If you want to keep your containers and data, please back up the $STORAGE_PATH directory before proceeding."


echo " Uninstall Gocker successfully!"