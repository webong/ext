#!/usr/bin/env bash
# native/linux-desktop: drafted from .github/workflows/native.yml (job linux-desktop).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-go@v6)

# Install isolated native desktop test dependencies
(
sudo apt-get update
sudo apt-get install -y pipewire pipewire-bin wireplumber dbus-x11 sway grim wf-recorder
)
ci_import_env

# Verify real PipeWire and Wayland sessions
(
scripts/verify-linux-desktop.sh
)
ci_import_env
