#!/usr/bin/env bash
# Go plugin modules and embedding examples.
. "$(dirname "$0")/../lib.sh"

for module in pkg/ext-go pkg/ext-go/engine pkg/ext-go/cgoengine pkg/ext-go/hashicorp pkg/ext-go/wasm pkg/ext-go/yaegi pkg/ext-go/runtime res/web/host; do
  (cd "$module" && go test -race ./...)
done
go test -race ./pkg/ext-go/hashicorp/cmd/ext-plugin

go run ./pkg/ext-go/examples/plugin-sdk --backend jsonline
go run ./pkg/ext-go/examples/plugin-sdk --backend inprocess
go run ./pkg/ext-go/hashicorp/cmd/ext-plugin inspect --root pkg/ext-go/examples/plugin-package --entry main pkg/ext-go/examples/plugin-package/plugin.json
