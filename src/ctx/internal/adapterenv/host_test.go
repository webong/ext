package adapterenv

import (
	"os"
	"reflect"
	"testing"
)

func TestHostDispatcherReplacesInheritedHints(t *testing.T) {
	input := []string{"PATH=/bin", "EXT_EXECUTABLE=forged", "ext_executable=duplicate", "EXT_PRODUCT_PREFIX=wrong"}
	before := append([]string(nil), input...)
	t.Setenv("EXT_HOST_EXECUTABLE", "/verified/ext")
	want := []string{"PATH=/bin", "EXT_EXECUTABLE=/verified/ext", "EXT_PRODUCT_PREFIX=ctx"}
	if got := Host(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("host: %v", got)
	}
	if !reflect.DeepEqual(input, before) {
		t.Fatal("input changed")
	}
	t.Setenv("EXT_HOST_EXECUTABLE", "")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got := Host(input); !reflect.DeepEqual(got, []string{"PATH=/bin", "EXT_EXECUTABLE=" + self}) {
		t.Fatalf("direct host: %v", got)
	}
}
