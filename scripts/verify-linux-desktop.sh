#!/usr/bin/env bash
# Isolated real PipeWire and wlroots session, with virtual audio and displays.
# Requires pipewire-bin, wireplumber, dbus, sway, grim and wf-recorder.
set -euo pipefail
cd "$(dirname "$0")/.."
[[ "$(uname -s)" == Linux && "$(id -u)" != 0 ]] || { echo 'run as a non-root Linux user' >&2; exit 1; }
if [[ "${1:-}" != --session ]]; then
  exec dbus-run-session -- "$0" --session
fi
export XDG_RUNTIME_DIR
XDG_RUNTIME_DIR="$(mktemp -d)"
export PIPEWIRE_RUNTIME_DIR="$XDG_RUNTIME_DIR" PIPEWIRE_REMOTE=pipewire-0
pids=()
cleanup() {
  result=$?
  set +e
  if [[ $result != 0 ]]; then tail -n 30 "$XDG_RUNTIME_DIR"/*.log; fi
  for pid in "${pids[@]}"; do kill "$pid" 2>/dev/null || true; done
  for pid in "${pids[@]}"; do wait "$pid" 2>/dev/null || true; done
  rm -rf "$XDG_RUNTIME_DIR"
}
trap cleanup EXIT
sed 's/#audiotestsrc/audiotestsrc/' /usr/share/pipewire/pipewire.conf >"$XDG_RUNTIME_DIR/pipewire.conf"
PIPEWIRE_CONFIG_DIR="$XDG_RUNTIME_DIR" pipewire >"$XDG_RUNTIME_DIR/pipewire.log" 2>&1 & pids+=("$!")
wireplumber >"$XDG_RUNTIME_DIR/wireplumber.log" 2>&1 & pids+=("$!")
for _ in {1..100}; do
  if pw-cli info 0 >/dev/null 2>&1; then break; fi
  sleep 0.1
done
pw-cli info 0 >/dev/null
pw-cli create-node adapter '{ factory.name=audiotestsrc node.name=ext-ci-input media.class=Audio/Source audio.position=[ FL FR ] object.linger=true }'
pw-cli create-node adapter '{ factory.name=support.null-audio-sink node.name=ext-ci-output media.class=Audio/Sink audio.position=[ FL FR ] object.linger=true node.driver=true }'
unset WAYLAND_DISPLAY
printf 'xwayland disable\noutput HEADLESS-1 resolution 640x480\nseat seat0 fallback true\n' >"$XDG_RUNTIME_DIR/sway.conf"
WLR_BACKENDS=headless WLR_RENDERER=pixman WLR_LIBINPUT_NO_DEVICES=1 sway -c "$XDG_RUNTIME_DIR/sway.conf" >"$XDG_RUNTIME_DIR/sway.log" 2>&1 & pids+=("$!")
for _ in {1..100}; do
  for socket in "$XDG_RUNTIME_DIR"/wayland-*; do
    if [[ -S "$socket" ]]; then export WAYLAND_DISPLAY="${socket##*/}"; break 2; fi
  done
  sleep 0.1
done
if [[ -z "${WAYLAND_DISPLAY:-}" ]]; then cat "$XDG_RUNTIME_DIR/sway.log"; exit 1; fi
export EXT_PIPEWIRE_NATIVE_TESTS=1 EXT_PIPEWIRE_TEST_INPUT=ext-ci-input EXT_PIPEWIRE_TEST_OUTPUT=ext-ci-output
export EXT_WAYLAND_NATIVE_TESTS=1 EXT_WAYLAND_TEST_OUTPUT=HEADLESS-1
# Precompiled test binaries allow the same verification inside a minimal container.
if [[ -n "${EXT_PIPEWIRE_TEST_BINARY:-}" ]]; then
  "$EXT_PIPEWIRE_TEST_BINARY" -test.v -test.run '^TestNativePipeWire' -test.timeout 45s
  "$EXT_WAYLAND_TEST_BINARY" -test.v -test.run '^TestNativeWayland' -test.timeout 45s
else
  go test -v ./adapters/pipewire ./adapters/wayland -run '^TestNative' -count=1 -timeout=45s
fi
