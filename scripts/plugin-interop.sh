#!/usr/bin/env bash
# Full interoperability proof. Requires the toolchains listed by the C runner.
set -euo pipefail
cd "$(dirname "$0")/.."
build_dir="${EXT_INTEROP_BUILD_DIR:-}"
if [[ -z "$build_dir" ]]; then build_dir="$(mktemp -d "${TMPDIR:-/tmp}/ext-interop.XXXXXX")"; fi
mkdir -p "$build_dir"
build_dir="$(cd "$build_dir" && pwd -P)"
export EXT_CENGINE_BUILD_DIR="${EXT_CENGINE_BUILD_DIR:-$build_dir/pkg/plugin-engine}"
if [[ "${EXT_INTEROP_REUSE_CENGINE:-0}" != 1 ]]; then
  EXT_CENGINE_SKIP_BENCH=1 scripts/plugin-cengine.sh
fi
go_tags=(-tags "")
if [[ "${EXT_INTEROP_ENGINE:-go}" == c ]]; then
  go_tags=(-tags ext_cengine)
  if [[ "${EXT_CENGINE_LINKAGE:-static}" == shared ]]; then go_tags=(-tags ext_cengine,ext_cengine_shared); fi
  export CGO_LDFLAGS="${CGO_LDFLAGS:-} -L$EXT_CENGINE_BUILD_DIR/release -Wl,-rpath,$EXT_CENGINE_BUILD_DIR/release"
fi
export EXT_BRIDGE_REQUIRED=1
export EXT_BRIDGE_EXECUTABLE="$build_dir/ext-plugin-bridge"
export EXT_BRIDGE_GUEST="$build_dir/hashicorp-guest"
export EXT_BRIDGE_JSONLINE_GUEST="$EXT_CENGINE_BUILD_DIR/go-guest"
go build "${go_tags[@]}" -o "$EXT_BRIDGE_EXECUTABLE" ./pkg/plugin-hashicorp/cmd/ext-plugin-bridge
go build -o "$EXT_BRIDGE_GUEST" ./pkg/plugin/bridge/testdata/hashicorp-guest
go test "${go_tags[@]}" -race -count=1 -timeout=120s ./pkg/plugin/bridge ./pkg/plugin-hashicorp/interoptest ./pkg/plugin-hashicorp/cmd/ext-plugin-bridge
(cd pkg/plugin-hashicorp && go test "${go_tags[@]}" -race -count=1 -timeout=120s ./...)
python3 - "$build_dir" "$EXT_CENGINE_BUILD_DIR" <<'PY'
import datetime,hashlib,json,pathlib,shutil,sys
p=pathlib.Path(sys.argv[1]); fixtures=pathlib.Path(sys.argv[2]); descriptor=json.loads((fixtures/'descriptor.json').read_text())
(p/'descriptor.json').write_text(json.dumps(descriptor))
for operation in ('echo','public-error'):
 request=dict(apiVersion='ext.plugin/v1',id='1',plugin=descriptor['identity'],contract=dict(name='ext.conformance',version='v1'),operation=operation,deadline='2099-01-01T00:00:00Z',payload=dict(value=7))
 (p/(operation+'.json')).write_text(json.dumps(request))
guest=p/'hashicorp-guest'
for protocol in ('grpc','netrpc'):
 executable=p/('bridge-'+protocol);shutil.copy2(p/'ext-plugin-bridge',executable)
 config=dict(apiVersion='ext.bridge/v1',descriptor=descriptor,process=dict(executable=str(guest),sha256=hashlib.sha256(guest.read_bytes()).hexdigest(),protocol=protocol,arguments=['--protocol',protocol]),allow=[dict(contract=dict(name=c['name'],version=c['version']),operation=o['name']) for c in descriptor['contracts'] for o in c['operations']])
 pathlib.Path(str(executable)+'.json').write_text(json.dumps(config))
PY
for protocol in grpc netrpc; do
  executable="$build_dir/bridge-$protocol"
  inputs=("$executable" "$build_dir/descriptor.json" "$build_dir/echo.json" "$build_dir/public-error.json")
  "$EXT_CENGINE_BUILD_DIR/go-host" "${inputs[@]}" > "$build_dir/go-$protocol.jsonl"
  rust_host="${CARGO_TARGET_DIR:-$EXT_CENGINE_BUILD_DIR/cargo}/debug/ext-cengine-example"
  "$rust_host" "${inputs[@]}" > "$build_dir/rust-$protocol.jsonl"
  "$EXT_CENGINE_BUILD_DIR/zig-host" "${inputs[@]}" > "$build_dir/zig-$protocol.jsonl"
  node pkg/plugin-ts/examples/cengine/host.cjs "$EXT_CENGINE_BUILD_DIR/ext_host.node" "${inputs[@]}" > "$build_dir/node-$protocol.jsonl"
  node pkg/plugin/bridge/testdata/ts-host.mjs "$executable" "$build_dir/descriptor.json"
  if [[ -n "${EXT_INTEROP_RUST_HOST:-}" ]]; then "$EXT_INTEROP_RUST_HOST" process "$executable"; fi
  if [[ -n "${EXT_INTEROP_ZIG_HOST:-}" ]]; then python3 pkg/plugin/bridge/testdata/zig_host.py "$EXT_INTEROP_ZIG_HOST" "$executable"; fi
done
python3 - "$build_dir" <<'PY'
import json,pathlib,sys
p=pathlib.Path(sys.argv[1])
for language in ('go','rust','zig','node'):
 for protocol in ('grpc','netrpc'):
  rows=[json.loads(line) for line in (p/(language+'-'+protocol+'.jsonl')).read_text().splitlines()]
  assert len(rows)==2 and rows[0]['payload']=={'value':7},(language,protocol,rows)
  assert rows[1]['error']['code']=='busy' and rows[1]['error']['retryAfterMilliseconds']==10,(language,protocol,rows)
print('Four host bindings reached both HashiCorp transports through the common CTX contract.')
PY
# Reverse direction: a HashiCorp frontend relays to independently authored guests.
for guest in "${EXT_CENGINE_RUST_GUEST:-}" "${EXT_CENGINE_ZIG_GUEST:-}"; do
  if [[ -n "$guest" ]]; then EXT_BRIDGE_JSONLINE_GUEST="$guest" go test "${go_tags[@]}" -race -count=1 -timeout=90s -run '^TestReverseHashicorpFrontend$' ./pkg/plugin-hashicorp/cmd/ext-plugin-bridge; fi
done
echo "Interoperability artifacts: $build_dir"
