#!/bin/sh
set -e

if ! id -u dockhand > /dev/null 2>&1; then
    useradd --system --no-create-home --shell /usr/sbin/nologin \
            --comment "Dockhand daemon" dockhand
fi

if getent group docker > /dev/null 2>&1; then
    usermod -aG docker dockhand
fi

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
    systemctl enable dockhand.service
    systemctl start dockhand.service || true
fi
