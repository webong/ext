//go:build ctx_cengine && cgo && (darwin || linux)

// Benchmark driver. Each invocation measures one engine/size in a fresh host.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/webong/ctx/pkg/go"
	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/jsonline"
	"github.com/webong/ctx/pkg/plugin/plugintest"
)

type process struct {
	io.ReadCloser
	io.WriteCloser
	cmd  *exec.Cmd
	once sync.Once
}

func (p *process) Close() error {
	p.once.Do(func() {
		_ = p.WriteCloser.Close()
		_ = p.ReadCloser.Close()
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	})
	return nil
}
func launch(path string) (plugin.Backend, error) {
	cmd := exec.Command(path)
	cmd.Env = []string{}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		_ = out.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		_ = in.Close()
		_ = out.Close()
		return nil, err
	}
	return jsonline.NewClient(&process{ReadCloser: out, WriteCloser: in, cmd: cmd}), nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 6 {
		return fmt.Errorf("expected engine guest payload-bytes iterations concurrency")
	}
	engine, path := os.Args[1], os.Args[2]
	size, err := strconv.Atoi(os.Args[3])
	if err != nil {
		return err
	}
	iterations, err := strconv.Atoi(os.Args[4])
	if err != nil {
		return err
	}
	concurrency, err := strconv.Atoi(os.Args[5])
	if err != nil {
		return err
	}
	if size < 0 || iterations < 1 || concurrency < 1 {
		return fmt.Errorf("invalid benchmark dimensions")
	}
	payload, _ := json.Marshal(strings.Repeat("x", size))
	d := plugintest.Descriptor()
	ctx := context.Background()
	ref := d.Contracts[0].ContractRef
	var call func() error
	var closeHost func()
	start := time.Now()
	switch engine {
	case "c":
		h, err := goengine.New(path, d, func([]byte) error { return nil }, func([]byte) error { return nil })
		if err != nil {
			return err
		}
		closeHost = h.Destroy
		if err = h.Start(ctx); err != nil {
			h.Destroy()
			return err
		}
		call = func() error {
			request, err := json.Marshal(plugin.Request{APIVersion: plugin.APIVersion, ID: "benchmark", Plugin: d.Identity, Contract: ref, Operation: "echo", Deadline: time.Now().Add(30 * time.Second), Payload: payload})
			if err != nil {
				return err
			}
			raw, err := h.CallRaw(ctx, request)
			if err != nil {
				return err
			}
			var response plugin.Response
			if err = json.Unmarshal(raw, &response); err != nil {
				return err
			}
			if string(response.Payload) != string(payload) {
				return fmt.Errorf("echo mismatch")
			}
			return nil
		}
	case "go":
		s, err := plugin.Open(ctx, d, plugin.Options{Verify: func(context.Context, plugin.Descriptor) error { return nil }, Authorize: func(context.Context, plugin.Request) error { return nil }, Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return launch(path) }})
		if err != nil {
			return err
		}
		closeHost = func() { _ = s.Abort() }
		call = func() error {
			out, err := s.Call(ctx, ref, "echo", payload)
			if err != nil {
				return err
			}
			if string(out) != string(payload) {
				return fmt.Errorf("echo mismatch")
			}
			return nil
		}
	default:
		return fmt.Errorf("unknown engine")
	}
	startup := time.Since(start)
	defer closeHost()
	for i := 0; i < 10; i++ {
		if err := call(); err != nil {
			return err
		}
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	samples := make([]int64, iterations)
	errs := make(chan error, concurrency)
	var wg sync.WaitGroup
	start = time.Now()
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := worker; i < iterations; i += concurrency {
				t := time.Now()
				if err := call(); err != nil {
					errs <- err
					return
				}
				samples[i] = time.Since(t).Nanoseconds()
			}
		}(worker)
	}
	wg.Wait()
	elapsed := time.Since(start)
	close(errs)
	for err := range errs {
		return err
	}
	runtime.ReadMemStats(&after)
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return err
	}
	rss := usage.Maxrss
	if runtime.GOOS == "linux" {
		rss *= 1024
	}
	report := map[string]any{"engine": engine, "payload_bytes": size, "iterations": iterations, "concurrency": concurrency, "startup_us": float64(startup.Nanoseconds()) / 1000, "p50_us": float64(samples[len(samples)/2]) / 1000, "p95_us": float64(samples[(len(samples)-1)*95/100]) / 1000, "calls_per_second": float64(iterations) / elapsed.Seconds(), "host_peak_rss_bytes": rss, "go_allocated_bytes_per_call": float64(after.TotalAlloc-before.TotalAlloc) / float64(iterations), "go_version": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH}
	return json.NewEncoder(os.Stdout).Encode(report)
}
