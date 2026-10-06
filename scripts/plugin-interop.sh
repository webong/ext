#!/usr/bin/env bash
# Full interoperability proof. Requires the toolchains listed by the C runner.
set -euo pipefail
cd "$(dirname "$0")/.."
build_dir="${CTX_INTEROP_BUILD_DIR:-}"
if [[ -z "$build_dir" ]]; then build_dir="$(mktemp -d "${TMPDIR:-/tmp}/ctx-interop.XXXXXX")"; fi
mkdir -p "$build_dir"
build_dir="$(cd "$build_dir" && pwd -P)"
export CTX_CENGINE_BUILD_DIR="${CTX_CENGINE_BUILD_DIR:-$build_dir/pkg/plugin-engine}"
if [[ "${CTX_INTEROP_REUSE_CENGINE:-0}" != 1 ]]; then
  CTX_CENGINE_SKIP_BENCH=1 scripts/plugin-cengine.sh
fi
go_tags=(-tags "")
if [[ "${CTX_INTEROP_ENGINE:-go}" == c ]]; then
  go_tags=(-tags ctx_cengine)
  if [[ "${CTX_CENGINE_LINKAGE:-static}" == shared ]]; then go_tags=(-tags ctx_cengine,ctx_cengine_shared); fi
  export CGO_LDFLAGS="${CGO_LDFLAGS:-} -L$CTX_CENGINE_BUILD_DIR/release -Wl,-rpath,$CTX_CENGINE_BUILD_DIR/release"
fi
export CTX_BRIDGE_REQUIRED=1
export CTX_BRIDGE_EXECUTABLE="$build_dir/ctx-plugin-bridge"
export CTX_BRIDGE_GUEST="$build_dir/hashicorp-guest"
export CTX_BRIDGE_JSONLINE_GUEST="$CTX_CENGINE_BUILD_DIR/go-guest"
go build "${go_tags[@]}" -o "$CTX_BRIDGE_EXECUTABLE" ./pkg/plugin-hashicorp/cmd/ctx-plugin-bridge
go build -o "$CTX_BRIDGE_GUEST" ./pkg/plugin/bridge/testdata/hashicorp-guest
go test "${go_tags[@]}" -race -count=1 -timeout=120s ./pkg/plugin/bridge ./pkg/plugin-hashicorp/interoptest ./pkg/plugin-hashicorp/cmd/ctx-plugin-bridge
(cd pkg/plugin-hashicorp && go test "${go_tags[@]}" -race -count=1 -timeout=120s ./...)
python3 - "$build_dir" "$CTX_CENGINE_BUILD_DIR" <<'PY'
import datetime,hashlib,json,pathlib,shutil,sys
p=pathlib.Path(sys.argv[1]); fixtures=pathlib.Path(sys.argv[2]); descriptor=json.loads((fixtures/'descriptor.json').read_text())
(p/'descriptor.json').write_text(json.dumps(descriptor))
for operation in ('echo','public-error'):
 request=dict(apiVersion='ctx.plugin/v1',id='1',plugin=descriptor['identity'],contract=dict(name='ctx.conformance',version='v1'),operation=operation,deadline='2099-01-01T00:00:00Z',payload=dict(value=7))
 (p/(operation+'.json')).write_text(json.dumps(request))
guest=p/'hashicorp-guest'
for protocol in ('grpc','netrpc'):
 executable=p/('bridge-'+protocol);shutil.copy2(p/'ctx-plugin-bridge',executable)
 config=dict(apiVersion='ctx.bridge/v1',descriptor=descriptor,process=dict(executable=str(guest),sha256=hashlib.sha256(guest.read_bytes()).hexdigest(),protocol=protocol,arguments=['--protocol',protocol]),allow=[dict(contract=dict(name=c['name'],version=c['version']),operation=o['name']) for c in descriptor['contracts'] for o in c['operations']])
 pathlib.Path(str(executable)+'.json').write_text(json.dumps(config))
PY
for protocol in grpc netrpc; do
  executable="$build_dir/bridge-$protocol"
  inputs=("$executable" "$build_dir/descriptor.json" "$build_dir/echo.json" "$build_dir/public-error.json")
  "$CTX_CENGINE_BUILD_DIR/go-host" "${inputs[@]}" > "$build_dir/go-$protocol.jsonl"
  rust_host="${CARGO_TARGET_DIR:-$CTX_CENGINE_BUILD_DIR/cargo}/debug/ctx-pkg/plugin-engine-example"
  "$rust_host" "${inputs[@]}" > "$build_dir/rust-$protocol.jsonl"
  "$CTX_CENGINE_BUILD_DIR/zig-host" "${inputs[@]}" > "$build_dir/zig-$protocol.jsonl"
  node pkg/plugin-ts/examples/cengine/host.cjs "$CTX_CENGINE_BUILD_DIR/ctx_host.node" "${inputs[@]}" > "$build_dir/node-$protocol.jsonl"
  node pkg/plugin/bridge/testdata/ts-host.mjs "$executable" "$build_dir/descriptor.json"
  if [[ -n "${CTX_INTEROP_RUST_HOST:-}" ]]; then "$CTX_INTEROP_RUST_HOST" process "$executable"; fi
  if [[ -n "${CTX_INTEROP_ZIG_HOST:-}" ]]; then python3 pkg/plugin/bridge/testdata/zig_host.py "$CTX_INTEROP_ZIG_HOST" "$executable"; fi
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
for guest in "${CTX_CENGINE_RUST_GUEST:-}" "${CTX_CENGINE_ZIG_GUEST:-}"; do
  if [[ -n "$guest" ]]; then CTX_BRIDGE_JSONLINE_GUEST="$guest" go test "${go_tags[@]}" -race -count=1 -timeout=90s -run '^TestReverseHashicorpFrontend$' ./pkg/plugin-hashicorp/cmd/ctx-plugin-bridge; fi
done
echo "Interoperability artifacts: $build_dir"
