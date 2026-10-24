package c2goengine

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/webong/ext/pkg/plugin-go/contract/plugintest/enginetest"
)

func engine() enginetest.Engine {
	return enginetest.Engine{
		Name:     "c2go engine",
		Fixtures: "../../../lib/plugin/testdata",
		Call:     EngineCall,
	}
}

// TestEngineChecks holds the translated engine against the pure-Go contract, with every check the
// harness has for the engine's services.
func TestEngineChecks(t *testing.T) {
	if !Available {
		t.Skip("no translation for this platform")
	}
	enginetest.Run(t, engine())
}

// TestConcurrentCalls makes the same calls from many goroutines and requires each to give what it
// gives alone: the translated code keeps no state between calls.
func TestConcurrentCalls(t *testing.T) {
	if !Available {
		t.Skip("no translation for this platform")
	}
	type call struct {
		operation string
		input     string
	}
	calls := []call{
		{"schema.check", `{"schema":{"type":"object","properties":{"a":{"type":"integer"}},"required":["a"]},"value":{"a":7}}`},
		{"schema.check", `{"schema":{"type":"object","properties":{"a":{"type":"integer"}},"required":["a"]},"value":{"a":"x"}}`},
		{"descriptor.validate", `{"apiVersion":"ext.plugin/v1","identity":{"id":"example/a","revision":"r1"},"contracts":[{"name":"example.a","version":"v1","operations":[{"name":"read"}]}]}`},
		{"descriptor.validate", `{"apiVersion":"ext.plugin/v2"}`},
		{"endpoint.check", `{"scheme":"http","address":"127.0.0.1:1","path":"/p","token":"AAAAAAAAAAAAAAAAAAAAAA","expires":"2099-01-01T00:00:00Z"}`},
	}
	want := make([]string, len(calls))
	for i, c := range calls {
		out, err := EngineCall(c.operation, json.RawMessage(c.input))
		want[i] = fmt.Sprintf("%s|%v", out, err)
	}
	var wg sync.WaitGroup
	failures := make(chan string, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 300; round++ {
				for i, c := range calls {
					out, err := EngineCall(c.operation, json.RawMessage(c.input))
					if got := fmt.Sprintf("%s|%v", out, err); got != want[i] {
						select {
						case failures <- fmt.Sprintf("%s: %s, alone %s", c.operation, got, want[i]):
						default:
						}
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for f := range failures {
		t.Error(f)
	}
}

// TestMemoryDoesNotGrow calls the engine many times and requires the process not to grow by more
// than a small margin: the buffers handed to the translated code are all released.
func TestMemoryDoesNotGrow(t *testing.T) {
	if !Available {
		t.Skip("no translation for this platform")
	}
	peak, ok := peakResidentBytes()
	if !ok {
		t.Skip("no resident size on this platform")
	}
	input := json.RawMessage(`{"schema":{"type":"array","items":{"type":"string","maxLength":16}},"value":["a","bb","ccc","dddd"]}`)
	run := func(n int) {
		for i := 0; i < n; i++ {
			if _, err := EngineCall("schema.check", input); err != nil {
				t.Fatal(err)
			}
		}
	}
	run(20000) // warm up: the first calls size the allocator and the thread state
	before, _ := peakResidentBytes()
	run(200000)
	after, _ := peakResidentBytes()
	_ = peak
	if grew := int64(after) - int64(before); grew > 16<<20 {
		t.Fatalf("the process grew by %d MiB over 200,000 calls", grew>>20)
	}
}

func BenchmarkDescriptorValidate(b *testing.B) {
	if !Available {
		b.Skip("no translation for this platform")
	}
	input := json.RawMessage(`{"apiVersion":"ext.plugin/v1","identity":{"id":"example/a","revision":"r1"},"contracts":[{"name":"example.a","version":"v1","operations":[{"name":"read"},{"name":"write"}]}]}`)
	b.SetBytes(int64(len(input)))
	for i := 0; i < b.N; i++ {
		if _, err := EngineCall("descriptor.validate", input); err != nil {
			b.Fatal(err)
		}
	}
}
