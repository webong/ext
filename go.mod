module github.com/webong/ctx

go 1.23.0

require (
	github.com/gorilla/websocket v1.5.3
	github.com/webong/ctx/pkg/adapter v0.1.0
	github.com/webong/ctx/pkg/go v0.1.0
	github.com/webong/ctx/pkg/graph v0.1.0
	github.com/webong/ctx/pkg/plugin v0.1.0
	github.com/webong/ctx/pkg/plugin/hashicorp v0.1.0
	github.com/webong/ctx/pkg/plugin/wasm v0.1.0
	github.com/webong/ctx/res/browser v0.1.0
	github.com/webong/ctx/res/credential v0.1.0
	github.com/hashicorp/go-hclog v0.14.1
	github.com/hashicorp/go-plugin v1.6.3
	github.com/tetratelabs/wazero v1.10.1
	google.golang.org/grpc v1.58.3
	google.golang.org/protobuf v1.36.1
)

require (
	github.com/fatih/color v1.7.0 // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/hashicorp/yamux v0.1.1 // indirect
	github.com/mattn/go-colorable v0.1.4 // indirect
	github.com/mattn/go-isatty v0.0.17 // indirect
	github.com/oklog/run v1.0.0 // indirect
	golang.org/x/net v0.34.0 // indirect
	golang.org/x/sys v0.29.0 // indirect
	golang.org/x/text v0.21.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20230711160842-782d3b101e98 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/webong/ctx/pkg/adapter => ./pkg/adapter

replace github.com/webong/ctx/pkg/go => ./pkg/go

replace github.com/webong/ctx/pkg/graph => ./pkg/graph

replace github.com/webong/ctx/pkg/plugin => ./pkg/plugin

replace github.com/webong/ctx/pkg/plugin/hashicorp => ./pkg/plugin/hashicorp

replace github.com/webong/ctx/pkg/plugin/wasm => ./pkg/plugin/wasm

replace github.com/webong/ctx/res/browser => ./res/browser

replace github.com/webong/ctx/res/credential => ./res/credential
