package c2goengine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/instance"
	"github.com/webong/ext/pkg/plugin-go/contract/plugintest/enginetest"
)

func engine() enginetest.Engine {
	e := enginetest.Engine{
		Name:     "c2go engine",
		Fixtures: "../../../lib/plugin/testdata",
		Call:     EngineCall,
	}
	if HostAvailable {
		e.Open = func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (enginetest.Session, error) {
			s, err := Open(ctx, d, o)
			if err != nil {
				return nil, err
			}
			return s, nil
		}
		e.NewInstances = func(opts instance.Options[string]) (enginetest.Instances, error) {
			m, err := NewInstances(opts)
			if err != nil {
				return nil, err
			}
			return engineInstances{m}, nil
		}
		e.OpenProcess = func(ctx context.Context, d plugin.Descriptor, l enginetest.Launch, o plugin.Options) (enginetest.Session, error) {
			s, err := OpenProcess(ctx, d, LaunchOptions{ProcessOptions: ProcessOptions{Executable: l.Executable, Environment: l.Environment}}, o)
			if err != nil {
				return nil, err
			}
			return s, nil
		}
	}
	return e
}

// engineInstances gives the translated manager the shape the harness compares.
type engineInstances struct{ *Instances[string] }

func (e engineInstances) Acquire(key string) (string, enginetest.Lease, error) {
	l, err := e.Instances.Acquire(key)
	if err != nil {
		return "", nil, err
	}
	return l.Value, l, nil
}

func TestMain(m *testing.M) {
	enginetest.MaybeServeGuest()
	os.Exit(m.Run())
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
