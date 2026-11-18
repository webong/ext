#!/usr/bin/env bash
# plugin-wamr/emulator: drafted from .github/workflows/plugin-wamr.yml (job emulator).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-java@v5)

# (action dtolnay/rust-toolchain@stable)

# (action actions/cache@v5)

# (action gradle/actions/setup-gradle@v4)

# Enable KVM
(
echo 'KERNEL=="kvm", GROUP="kvm", MODE="0666", OPTIONS+="static_node=kvm"' | sudo tee /etc/udev/rules.d/99-kvm4all.rules
sudo udevadm control --reload-rules
sudo udevadm trigger --name-match=kvm
)
ci_import_env

# (action reactivecircus/android-emulator-runner@v2.33.0)
