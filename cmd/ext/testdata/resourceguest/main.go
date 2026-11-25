package main

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/jsonline"
)

var marker string
var dependencies string

func main() {
	if p := marker; p != "" {
		if executable, err := os.Executable(); err == nil {
			_ = os.WriteFile(marker+".executable", []byte(executable), 0600)
		}
		_ = os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())), 0600)
	}
	identity := plugin.Identity{ID: "example/resources", Revision: "resource-v1", Version: "1.0.0"}
	d := plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: identity, Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "ext.resource", Version: "v1"}, Operations: []plugin.Operation{{Name: "list"}, {Name: "inspect"}}}}}
	d.Contracts = append(d.Contracts, plugin.Contract{ContractRef: plugin.ContractRef{Name: "example.record", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}})
	if dependencies != "" {
		d.Reverse = []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.dependent", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}
	}
	err := jsonline.ServeFunc(context.Background(), d, func(ctx context.Context, method string, payload json.RawMessage) (any, error) {
		if method == "example.record.read" {
			if marker != "" {
				_ = os.WriteFile(marker+".call", []byte("called"), 0600)
			}
			var call struct {
				Resource struct {
					Provider plugin.Identity
					ID       string
					Type     string
				}
				Input string
			}
			if err := json.Unmarshal(payload, &call); err != nil {
				return nil, err
			}
			if call.Resource.Provider != identity || call.Resource.Type != "example.record" {
				return nil, &plugin.RemoteError{Code: "denied", Message: "wrong resource authority"}
			}
			if call.Resource.ID == "denied-on-call" {
				return nil, &plugin.RemoteError{Code: "denied", Message: "resource no longer authorized"}
			}
			if call.Input == "bad-result" {
				return 42, nil
			}
			if dependencies != "" && (call.Input == "dependency" || call.Input == "dependency-wait") {
				input, _ := json.Marshal(map[string]bool{"wait": call.Input == "dependency-wait"})
				value, err := plugin.ReverseFrom(ctx).Call(ctx, plugin.ContractRef{Name: "example.dependent", Version: "v1"}, "read", input)
				if err != nil {
					return nil, err
				}
				return "dependency:" + string(value), nil
			}
			if call.Input == "wait-for-cancel" {
				_ = os.WriteFile(marker+".waiting", []byte(strconv.Itoa(os.Getpid())), 0600)
				time.Sleep(30 * time.Second)
			}
			return "value:" + call.Resource.ID + ":" + call.Input, nil
		}
		var req struct {
			Type, Cursor string
			Resource     json.RawMessage
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		if method == "ext.resource.inspect" {
			var resource struct{ ID string }
			if err := json.Unmarshal(req.Resource, &resource); err != nil {
				return nil, err
			}
			if resource.ID == "missing" {
				return nil, &plugin.RemoteError{Code: "not_found", Message: "resource not found"}
			}
			if resource.ID == "private" {
				return nil, &plugin.RemoteError{Code: "denied", Message: "resource access denied"}
			}

			inputSchema := map[string]any{"type": "object", "required": []string{"resource", "input"}, "properties": map[string]any{"resource": map[string]any{"type": "object", "additionalProperties": true}, "input": map[string]any{"type": "string"}}, "additionalProperties": false}
			return map[string]any{"resource": req.Resource, "operations": []any{map[string]any{"contract": map[string]string{"name": "example.record", "version": "v1"}, "operation": "read", "input": inputSchema, "output": map[string]string{"type": "string"}}}}, nil
		}
		if req.Type == "test.timeout" {
			time.Sleep(10 * time.Second)
		}
		if req.Type == "test.cancel" {
			_ = os.WriteFile(marker+".waiting", []byte(strconv.Itoa(os.Getpid())), 0600)
			time.Sleep(30 * time.Second)
		}
		ref := map[string]any{"provider": identity, "type": "example.record", "id": "one"}
		if req.Cursor == "domain-error" {
			return nil, &plugin.RemoteError{Code: "key_missing", Message: "not a discovery category"}
		}
		if req.Cursor == "expired" {
			return nil, &plugin.RemoteError{Code: "expired", Message: "continuation token expired"}
		}
		if req.Cursor == "bad" {
			return map[string]any{"items": []any{ref, ref}}, nil
		}
		if req.Cursor == "page2" {
			ref["id"] = "two"
			return map[string]any{"items": []any{ref}}, nil
		}
		return map[string]any{"items": []any{ref}, "next": "page2"}, nil
	}, jsonline.FuncOptions{})
	if err != nil {
		os.Exit(1)
	}
}
