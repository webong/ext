package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/webong/ext/pkg/graph"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("EXT_SUPERVISOR_HELPER") != "1" {
		return
	}
	if os.Getenv("EXT_SUPERVISOR_HOLD") == "1" {
		time.Sleep(30 * time.Second)
	}
	if count, _ := strconv.Atoi(os.Getenv("EXT_SUPERVISOR_OUTPUT_BYTES")); count > 0 {
		_, _ = os.Stdout.WriteString(strings.Repeat("x", count))
	}
	if os.Getenv("EXT_SUPERVISOR_ECHO_SECRET") == "1" {
		_, _ = os.Stdout.WriteString(os.Getenv("EXAMPLE_SECRET"))
	}
	if code, _ := strconv.Atoi(os.Getenv("EXT_SUPERVISOR_EXIT_CODE")); code != 0 {
		os.Exit(code)
	}
	_, _ = os.Stdout.WriteString("runtime-output\n")
	os.Exit(0)
}

func TestCrashRestartAndBoundedLogs(t *testing.T) {
	store := graph.NewMemory()
	defer store.Close()
	sup, err := New(Options{Graph: store, LogLimit: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer sup.Close(context.Background())
	instance, err := sup.Start(context.Background(), Spec{
		Artifact: Artifact{ID: "crashing", Revision: "1"}, Command: os.Args[0], Args: []string{"-test.run=TestHelperProcess"},
		Environment: map[string]string{"EXT_SUPERVISOR_HELPER": "1", "EXT_SUPERVISOR_EXIT_CODE": "7", "EXT_SUPERVISOR_OUTPUT_BYTES": "4096"},
		Restart:     RestartPolicy{MaxAttempts: 1, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		i, e := sup.Instance(instance.ID)
		return e == nil && i.State == StateCrashed && i.CrashCount == 2
	})
	logs, err := sup.Logs(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) > 128 {
		t.Fatalf("log tail exceeded limit: %d", len(logs))
	}
	process, err := store.GetVertex(context.Background(), RuntimeNamespace, "process/"+instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if process.Attributes["crash_count"] != float64(2) {
		t.Fatalf("crash fact missing: %#v", process.Attributes)
	}
}

func TestLifecycleFactsAndSecretReferences(t *testing.T) {
	ctx := context.Background()
	store := graph.NewMemory()
	defer store.Close()
	secret := "never-project-this-value"
	payload, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	checksum := "sha256:" + hex.EncodeToString(sum[:])
	sup, err := New(Options{Graph: store, SecretResolver: func(context.Context, SecretReference) (string, error) { return secret, nil }, LogLimit: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer sup.Close(context.Background())
	instance, err := sup.Start(ctx, Spec{Artifact: Artifact{ID: "artifact-1", Revision: "rev-3", Checksum: checksum}, Command: os.Args[0], Args: []string{"-test.run=TestHelperProcess"}, Environment: map[string]string{"EXT_SUPERVISOR_HELPER": "1", "EXT_SUPERVISOR_ECHO_SECRET": "1"}, SecretReferences: map[string]SecretReference{"EXAMPLE_SECRET": {Scope: "project:demo", ID: "db-password"}}, Endpoints: []Endpoint{{ID: "ipc-1", Address: "unix:///tmp/example.sock", Transport: "unix"}, {ID: "ipc-2", Address: "tcp://127.0.0.1:7000", Transport: "tcp"}}, Connections: []Connection{{ID: "link-1", SourceEndpoint: "ipc-1", TargetEndpoint: "ipc-2", Metadata: map[string]string{"channel": "control"}}}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { i, e := sup.Instance(instance.ID); return e == nil && i.State == StateStopped })
	logs, err := sup.Logs(instance.ID)
	if err != nil || logs != "" {
		t.Fatalf("secret-bearing process output must be discarded: %q, %v", logs, err)
	}
	process, err := store.GetVertex(ctx, RuntimeNamespace, "process/"+instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if process.Attributes["state"] != string(StateStopped) {
		t.Fatalf("graph did not receive stopped fact: %#v", process.Attributes)
	}
	if _, ok := process.Attributes["authorized"]; ok {
		t.Fatal("runtime fact implies domain authority")
	}
	snapshot, err := store.Snapshot(ctx, RuntimeNamespace)
	if err != nil {
		t.Fatal(err)
	}
	serialized := ""
	for _, v := range snapshot.Vertices {
		serialized += v.ID + " "
		for k, val := range v.Attributes {
			serialized += k + " "
			if x, ok := val.(string); ok {
				serialized += x + " "
			}
		}
	}
	if strings.Contains(serialized, secret) {
		t.Fatal("secret value was projected into graph")
	}
	if _, err = store.GetEdge(ctx, RuntimeNamespace, "spawned-by/"+instance.ID); err != nil {
		t.Fatalf("missing generic spawned-by relationship: %v", err)
	}
	if _, err = store.GetEdge(ctx, RuntimeNamespace, "connected-to/link-1"); err != nil {
		t.Fatalf("missing generic IPC connection fact: %v", err)
	}
}

func TestStopAndHealthProjection(t *testing.T) {
	store := graph.NewMemory()
	defer store.Close()
	sup, err := New(Options{Graph: store})
	if err != nil {
		t.Fatal(err)
	}
	defer sup.Close(context.Background())
	instance, err := sup.Start(context.Background(), Spec{Artifact: Artifact{ID: "long", Revision: "1"}, Command: os.Args[0], Args: []string{"-test.run=TestHelperProcess"}, Environment: map[string]string{"EXT_SUPERVISOR_HELPER": "1", "EXT_SUPERVISOR_HOLD": "1"}, HealthCheck: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { i, e := sup.Instance(instance.ID); return e == nil && i.State == StateReady })
	healthy, err := sup.Health(context.Background(), instance.ID)
	if err != nil || !healthy {
		t.Fatalf("health=%v err=%v", healthy, err)
	}
	if err = sup.Stop(context.Background(), instance.ID); err != nil {
		t.Fatal(err)
	}
	i, err := sup.Instance(instance.ID)
	if err != nil || i.State != StateStopped {
		t.Fatalf("stop lifecycle: %+v %v", i, err)
	}
	process, err := store.GetVertex(context.Background(), RuntimeNamespace, "process/"+instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if process.Attributes["health"] != "unhealthy" {
		t.Fatalf("health fact missing: %#v", process.Attributes)
	}
}

func TestArtifactChecksumRejectedBeforeStart(t *testing.T) {
	store := graph.NewMemory()
	defer store.Close()
	sup, err := New(Options{Graph: store, RequireChecksum: true})
	if err != nil {
		t.Fatal(err)
	}
	defer sup.Close(context.Background())
	spec := Spec{Artifact: Artifact{ID: "verified", Revision: "1"}, Command: os.Args[0], Args: []string{"-test.run=TestHelperProcess"}, Environment: map[string]string{"EXT_SUPERVISOR_HELPER": "1"}}
	if _, err := sup.Start(context.Background(), spec); err == nil {
		t.Fatal("required checksum was omitted")
	}
	spec.Artifact.Checksum = "sha256:" + strings.Repeat("0", 64)
	if _, err := sup.Start(context.Background(), spec); err == nil {
		t.Fatal("incorrect checksum was accepted")
	}
	if len(sup.Instances()) != 0 {
		t.Fatal("rejected artifact created a process instance")
	}
}

func TestEndpointAndHandshakeGateReadiness(t *testing.T) {
	store := graph.NewMemory()
	defer store.Close()
	sup, err := New(Options{Graph: store})
	if err != nil {
		t.Fatal(err)
	}
	defer sup.Close(context.Background())
	release := make(chan struct{})
	readyEntered := make(chan struct{})
	handshakeEntered := make(chan struct{})
	instance, err := sup.Start(context.Background(), Spec{
		Artifact: Artifact{ID: "ipc", Revision: "1"}, Command: os.Args[0], Args: []string{"-test.run=TestHelperProcess"},
		Environment: map[string]string{"EXT_SUPERVISOR_HELPER": "1", "EXT_SUPERVISOR_HOLD": "1"},
		EndpointReady: func(ctx context.Context, _ Instance) error {
			close(readyEntered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		ProtocolHandshake: func(context.Context, Instance) error { close(handshakeEntered); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-readyEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("readiness hook was not called")
	}
	select {
	case <-handshakeEntered:
		t.Fatal("handshake ran before endpoint readiness")
	default:
	}
	if healthy, err := sup.Health(context.Background(), instance.ID); err != nil || healthy {
		t.Fatalf("unready process health=%t err=%v", healthy, err)
	}
	close(release)
	waitFor(t, func() bool { i, e := sup.Instance(instance.ID); return e == nil && i.State == StateReady })
	select {
	case <-handshakeEntered:
	default:
		t.Fatal("protocol handshake was skipped")
	}
	if healthy, err := sup.Health(context.Background(), instance.ID); err != nil || !healthy {
		t.Fatalf("ready process health=%t err=%v", healthy, err)
	}
	if err := sup.Stop(context.Background(), instance.ID); err != nil {
		t.Fatal(err)
	}
	if healthy, err := sup.Health(context.Background(), instance.ID); err != nil || healthy {
		t.Fatalf("stopped process health=%t err=%v", healthy, err)
	}
}

func TestExpiredLeaseMarksOrphanAndBlocksStart(t *testing.T) {
	store := graph.NewMemory()
	defer store.Close()
	if err := store.Register(graph.Schema{Namespace: RuntimeNamespace, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	_, err := store.Apply(context.Background(), graph.Transaction{Namespace: RuntimeNamespace, Vertices: []graph.Vertex{{ID: "process/old", Kind: RuntimeNamespace + "/process-instance", Attributes: map[string]any{"runtime_id": "test-runtime", "state": string(StateRunning), "pid": os.Getpid(), "lease_until": time.Now().Add(-time.Minute).Format(time.RFC3339Nano)}}}})
	if err != nil {
		t.Fatal(err)
	}
	sup, err := New(Options{Graph: store, RuntimeID: "test-runtime", OrphanPolicy: OrphanMark})
	if err != nil {
		t.Fatal(err)
	}
	defer sup.Close(context.Background())
	if orphans := sup.Orphans(); len(orphans) != 1 || orphans[0].ID != "old" {
		t.Fatalf("recovered orphans: %+v", orphans)
	}
	if _, err := sup.Start(context.Background(), Spec{Artifact: Artifact{ID: "new", Revision: "1"}, Command: os.Args[0]}); err == nil {
		t.Fatal("new start bypassed unresolved orphan")
	}
	vertex, err := store.GetVertex(context.Background(), RuntimeNamespace, "process/old")
	if err != nil || vertex.Attributes["state"] != string(StateOrphaned) {
		t.Fatalf("orphan graph state: %+v, %v", vertex, err)
	}
}

func TestActiveLeaseRejectsSecondSupervisor(t *testing.T) {
	store := graph.NewMemory()
	defer store.Close()
	if err := store.Register(graph.Schema{Namespace: RuntimeNamespace, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	_, err := store.Apply(context.Background(), graph.Transaction{Namespace: RuntimeNamespace, Vertices: []graph.Vertex{{ID: "process/live", Kind: RuntimeNamespace + "/process-instance", Attributes: map[string]any{"runtime_id": "test-runtime", "state": string(StateRunning), "pid": os.Getpid(), "lease_until": time.Now().Add(time.Minute).Format(time.RFC3339Nano)}}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(Options{Graph: store, RuntimeID: "test-runtime"})
	if !errors.Is(err, ErrRuntimeLeased) {
		t.Fatalf("active lease: %v", err)
	}
}

func TestTerminalInstanceRetentionBoundsGraph(t *testing.T) {
	store := graph.NewMemory()
	defer store.Close()
	sup, err := New(Options{Graph: store, RetainTerminal: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer sup.Close(context.Background())
	for i := 0; i < 3; i++ {
		instance, err := sup.Start(context.Background(), Spec{Artifact: Artifact{ID: "short", Revision: "1"}, Command: os.Args[0], Args: []string{"-test.run=TestHelperProcess"}, Environment: map[string]string{"EXT_SUPERVISOR_HELPER": "1"}})
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool {
			vertex, err := store.GetVertex(context.Background(), RuntimeNamespace, "process/"+instance.ID)
			return err == nil && vertex.Attributes["state"] == string(StateStopped)
		})
	}
	waitFor(t, func() bool {
		snapshot, err := store.Snapshot(context.Background(), RuntimeNamespace)
		if err != nil {
			return false
		}
		processes := 0
		for _, vertex := range snapshot.Vertices {
			if vertex.Kind == RuntimeNamespace+"/process-instance" {
				processes++
			}
		}
		return processes == 1
	})
	waitFor(t, func() bool { return len(sup.Instances()) == 1 })
}

func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition timed out")
}
