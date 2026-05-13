#!/bin/sh
set -e

if [ -d /run/systemd/system ]; then
    systemctl stop dockhand.service || true
    systemctl disable dockhand.service || true
    systemctl daemon-reload
fi
