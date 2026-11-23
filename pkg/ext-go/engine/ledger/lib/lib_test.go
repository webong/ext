package lib_test

import (
	"sync"
	"testing"

	"github.com/webong/ext/pkg/ext-go/engine/ledger/lib"
)

func TestHandles(t *testing.T) {
	l := lib.New()
	s, err := l.Open([]byte(`{"object":"store"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Call(s, "register", []byte(`{"schema":{"namespace":"demo","version":"1"}}`)); err != nil {
		t.Fatal(err)
	}
	for config, want := range map[string]string{
		`{"object":"store","backend":"file"}`:                 "invalid",
		`{"object":"store","backend":"file","path":"/tmp/x"}`: "unsupported",
		`{"object":"recorder","store":1,"machine":true}`:      "unsupported",
		`{"object":"supervisor","store":99}`:                  "not_found",
		`{"object":"nothing"}`:                                "invalid",
		`not json`:                                            "invalid",
	} {
		if _, err := l.Open([]byte(config)); lib.Code(err) != want {
			t.Errorf("open %s: %v, want %s", config, err, want)
		}
	}
	// Calls from many goroutines on one store, then a close.
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := l.Call(s, "snapshot", []byte(`{"namespace":"demo"}`)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := l.Close(s); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Call(s, "snapshot", []byte(`{"namespace":"demo"}`)); lib.Code(err) != "not_found" {
		t.Errorf("a call on a closed handle: %v", err)
	}
	if _, err := l.Call(0, "code_name", []byte(`{"code":12}`)); err != nil {
		t.Error(err)
	}
}
