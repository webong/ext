package adapterenv

import (
	adapter "github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter"
	"reflect"
	"testing"
)

func TestDeclaredDependenciesReplaceInheritedHints(t *testing.T) {
	input := []string{"PATH=/bin", "EXT_DEPENDENCY_CREDENTIAL=forged", "ext_dependency_network=forged", "EXT_DEPENDENCY_CREDENTIAL=duplicate", "OTHER=kept"}
	before := append([]string(nil), input...)
	declared := map[string]adapter.Dependency{"credential": {Adapter: "vault"}, "network": {Adapter: "network"}}
	want := []string{"PATH=/bin", "OTHER=kept", "EXT_DEPENDENCY_CREDENTIAL=vault", "EXT_DEPENDENCY_NETWORK=network"}
	if got := Dependencies(input, declared); !reflect.DeepEqual(got, want) {
		t.Fatalf("environment: %v", got)
	}
	if !reflect.DeepEqual(input, before) {
		t.Fatal("changed caller environment")
	}
	if got := Dependencies(input, nil); !reflect.DeepEqual(got, []string{"PATH=/bin", "OTHER=kept"}) {
		t.Fatalf("undeclared hints retained: %v", got)
	}
}
