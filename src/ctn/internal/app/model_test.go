package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webong/ext/res/ml"
)

// modelStoreTestHelper creates a temp store with one installed model.
func modelStoreTestHelper(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	store, err := ml.OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	manifest := ml.Manifest{
		APIVersion:   ml.ManifestAPIVersion,
		Name:         "example-model",
		Version:      "v1.2.3",
		Provider:     "onnxruntime",
		Capabilities: []string{"image.classify"},
		Artifacts: []ml.Artifact{
			{Role: "model", File: "model.onnx", SHA256: sha256Hex("model bytes")},
		},
	}
	files := map[string][]byte{"model.onnx": []byte("model bytes")}
	if _, err := store.InstallFromBytes(context.Background(), manifest, files); err != nil {
		t.Fatal(err)
	}
	return root
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func TestModelCommandList(t *testing.T) {
	root := modelStoreTestHelper(t)
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"list", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("model list = %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "onnxruntime/example-model@v1.2.3") {
		t.Fatalf("list output = %q", stdout.String())
	}
}

func TestModelCommandListMissingDir(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"list", "/nonexistent-dir"}, &stdout, &stderr); code == 0 {
		t.Fatal("model list on missing dir should fail")
	}
}

func TestModelCommandInspect(t *testing.T) {
	root := modelStoreTestHelper(t)
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"inspect", root, "onnxruntime/example-model@v1.2.3"}, &stdout, &stderr); code != 0 {
		t.Fatalf("model inspect = %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "example-model") {
		t.Fatalf("inspect output = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "onnxruntime") {
		t.Fatalf("inspect output missing provider = %q", stdout.String())
	}
}

func TestModelCommandInspectMissingModel(t *testing.T) {
	root := modelStoreTestHelper(t)
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"inspect", root, "onnxruntime/missing@v9.9.9"}, &stdout, &stderr); code == 0 {
		t.Fatal("model inspect of missing model should fail")
	}
}

func TestModelCommandRunRequiresProvider(t *testing.T) {
	root := modelStoreTestHelper(t)
	inputFile := writeTensorInput(t, []mlTensorInput{{
		Shape:  []int{1, 4},
		Format: "float32",
		Data:   []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	}})
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"run", root, "onnxruntime/example-model@v1.2.3", inputFile}, &stdout, &stderr); code == 0 {
		t.Fatal("model run without --provider should fail")
	}
}

func TestModelCommandRunNoProviderAvailable(t *testing.T) {
	t.Setenv("EXT_HOME", t.TempDir())
	t.Setenv("EXT_INFERENCE_PACKAGE_ROOT", filepath.Join(t.TempDir(), "packages"))
	root := modelStoreTestHelper(t)
	inputFile := writeTensorInput(t, []mlTensorInput{{
		Shape:  []int{1, 4},
		Format: "float32",
		Data:   []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	}})
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"run", "--provider", "nonexistent", root, "onnxruntime/example-model@v1.2.3", inputFile}, &stdout, &stderr); code == 0 {
		t.Fatal("model run with missing provider should fail")
	}
	if !strings.Contains(stderr.String(), "not installed") && !strings.Contains(stderr.String(), "no such file") {
		t.Fatalf("expected missing provider package, got: %q", stderr.String())
	}
}

func TestModelRunThroughTrustedProviderPackage(t *testing.T) {
	modelRoot := modelStoreTestHelper(t)
	config := t.TempDir()
	t.Setenv("EXT_HOME", config)
	t.Setenv("EXT_INFERENCE_PACKAGE_ROOT", filepath.Join(config, "packages"))
	binary := filepath.Join(t.TempDir(), "modelguest")
	build := exec.Command("go", "build", "-o", binary, "./testdata/modelguest")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build model guest: %v\n%s", err, out)
	}
	packageDir := filepath.Join(t.TempDir(), "package")
	var stdout, stderr bytes.Buffer
	if code := modelProviderCommand([]string{"package", "onnxruntime", "1.0.0", binary, packageDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("package = %d: %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := modelProviderCommand([]string{"install", packageDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("install = %d: %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := modelProviderCommand([]string{"list"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "onnxruntime\t1.0.0\tuntrusted\t") {
		t.Fatalf("untrusted provider list = %d, %q, %q", code, stdout.String(), stderr.String())
	}
	input := writeTensorInput(t, []mlTensorInput{{Shape: []int{1}, Format: "float32", Data: []byte{0, 0, 128, 63}}})
	run := []string{"run", "--provider", "onnxruntime", modelRoot, "onnxruntime/example-model@v1.2.3", input}
	stdout.Reset()
	stderr.Reset()
	if code := modelCommand(run, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "not trusted") {
		t.Fatalf("untrusted run = %d, %q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := modelProviderCommand([]string{"trust", "onnxruntime"}, &stdout, &stderr); code != 0 {
		t.Fatalf("trust = %d: %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := modelProviderCommand([]string{"list"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "onnxruntime\t1.0.0\ttrusted\t") {
		t.Fatalf("trusted provider list = %d, %q, %q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := modelCommand(run, &stdout, &stderr); code != 0 {
		t.Fatalf("run = %d: %s", code, stderr.String())
	}
	var result struct {
		Tensors []ml.Tensor `json:"tensors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || len(result.Tensors) != 1 || len(result.Tensors[0].Data) != 4 {
		t.Fatalf("output = %s, %v", stdout.String(), err)
	}
	outputFile := filepath.Join(t.TempDir(), "output.json")
	fileRun := []string{"run", "--provider", "onnxruntime", "--output", outputFile,
		modelRoot, "onnxruntime/example-model@v1.2.3", input}
	stdout.Reset()
	stderr.Reset()
	if code := modelCommand(fileRun, &stdout, &stderr); code != 0 {
		t.Fatalf("file output = %d: %s", code, stderr.String())
	}
	if data, err := os.ReadFile(outputFile); err != nil || json.Unmarshal(data, &result) != nil {
		t.Fatalf("output file: %s, %v", data, err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := modelCommand(fileRun, &stdout, &stderr); code == 0 {
		t.Fatal("existing output file was overwritten")
	}
	stdout.Reset()
	stderr.Reset()
	if code := modelProviderCommand([]string{"untrust", "onnxruntime"}, &stdout, &stderr); code != 0 {
		t.Fatalf("untrust = %d: %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := modelCommand(run, &stdout, &stderr); code == 0 {
		t.Fatal("revoked provider was accepted")
	}
}

// The macOS CI job supplies the Swift executable. The tiny CoreML Copy
// model proves the whole Go host/Swift guest/native prediction path without
// downloading model weights or adding a binary fixture to this repository.
func TestModelRunThroughCoreMLGuest(t *testing.T) {
	binary := os.Getenv("EXT_COREML_GUEST_BINARY")
	if binary == "" {
		t.Skip("set EXT_COREML_GUEST_BINARY to the built Swift guest")
	}
	config := t.TempDir()
	t.Setenv("EXT_HOME", config)
	t.Setenv("EXT_INFERENCE_PACKAGE_ROOT", filepath.Join(config, "packages"))
	modelRoot := t.TempDir()
	modelStore, err := ml.OpenStore(modelRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer modelStore.Close()
	modelBytes := coreMLCopyModel()
	modelHash := sha256.Sum256(modelBytes)
	manifest := ml.Manifest{APIVersion: ml.ManifestAPIVersion, Provider: "coreml", Name: "identity", Version: "v1",
		Artifacts: []ml.Artifact{{Role: "model", File: "identity.mlmodel", SHA256: hex.EncodeToString(modelHash[:])}}}
	if _, err := modelStore.InstallFromBytes(context.Background(), manifest,
		map[string][]byte{"identity.mlmodel": modelBytes}); err != nil {
		t.Fatal(err)
	}
	packageDir := filepath.Join(t.TempDir(), "package")
	var stdout, stderr bytes.Buffer
	if code := modelProviderCommand([]string{"package", "coreml", "1.0.0", binary, packageDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("package Swift guest = %d: %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := modelProviderCommand([]string{"install", packageDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("install Swift guest = %d: %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := modelProviderCommand([]string{"trust", "coreml"}, &stdout, &stderr); code != 0 {
		t.Fatalf("trust Swift guest = %d: %s", code, stderr.String())
	}
	inputBytes := []byte{0, 0, 128, 63, 0, 0, 0, 64}
	input := writeTensorInput(t, []mlTensorInput{{Shape: []int{1, 2}, Format: "float32", Data: inputBytes}})
	stdout.Reset()
	stderr.Reset()
	code := modelCommand([]string{"run", "--provider", "coreml", modelRoot, "coreml/identity@v1", input}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Swift guest call = %d, %q", code, stderr.String())
	}
	var result struct {
		Tensors []ml.Tensor `json:"tensors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || len(result.Tensors) != 1 ||
		!bytes.Equal(result.Tensors[0].Data, inputBytes) {
		t.Fatalf("CoreML copy output = %q, %v", stdout.String(), err)
	}
}

// CoreML Model.proto defines NeuralNetwork at field 500, and NeuralNetwork.proto
// defines Copy at field 600. FeatureTypes.proto defines float32 arrays. This
// fixture uses those public wire fields directly to keep it small and stable.
func coreMLCopyModel() []byte {
	var number func(int, uint64) []byte
	varint := func(value uint64) []byte {
		var out []byte
		for value >= 0x80 {
			out = append(out, byte(value)|0x80)
			value >>= 7
		}
		return append(out, byte(value))
	}
	number = func(field int, value uint64) []byte {
		return append(varint(uint64(field)<<3), varint(value)...)
	}
	field := func(field int, value []byte) []byte {
		out := append(varint(uint64(field)<<3|2), varint(uint64(len(value)))...)
		return append(out, value...)
	}
	feature := func(name string) []byte {
		array := append(append(number(1, 1), number(1, 2)...), number(2, 65568)...)
		return append(field(1, []byte(name)), field(3, field(5, array))...)
	}
	description := append(field(1, feature("x")), field(10, feature("y"))...)
	layer := append(field(1, []byte("copy")), field(2, []byte("x"))...)
	layer = append(layer, field(3, []byte("y"))...)
	layer = append(layer, field(600, nil)...)
	network := append(field(1, layer), number(5, 1)...)
	model := append(number(1, 4), field(2, description)...)
	return append(model, field(500, network)...)
}

// Windows and Linux CI publish a self-contained ONNX guest. A tiny Identity graph
// exercises package trust, the .NET process, ONNX Runtime, and tensor output.
func TestModelRunThroughONNXGuest(t *testing.T) {
	binary := os.Getenv("EXT_ONNX_GUEST_BINARY")
	if binary == "" {
		t.Skip("set EXT_ONNX_GUEST_BINARY to the published .NET guest")
	}
	config := t.TempDir()
	t.Setenv("EXT_HOME", config)
	t.Setenv("EXT_INFERENCE_PACKAGE_ROOT", filepath.Join(config, "packages"))
	modelRoot := t.TempDir()
	modelStore, err := ml.OpenStore(modelRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer modelStore.Close()
	modelBytes := onnxIdentityModel()
	modelHash := sha256.Sum256(modelBytes)
	manifest := ml.Manifest{APIVersion: ml.ManifestAPIVersion, Provider: "onnxruntime", Name: "identity", Version: "v1",
		Artifacts: []ml.Artifact{{Role: "model", File: "identity.onnx", SHA256: hex.EncodeToString(modelHash[:])}}}
	if _, err := modelStore.InstallFromBytes(context.Background(), manifest,
		map[string][]byte{"identity.onnx": modelBytes}); err != nil {
		t.Fatal(err)
	}
	packageDir := filepath.Join(t.TempDir(), "package")
	var stdout, stderr bytes.Buffer
	if code := modelProviderCommand([]string{"package", "onnxruntime", "1.0.0", binary, packageDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("package .NET guest = %d: %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := modelProviderCommand([]string{"install", packageDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("install .NET guest = %d: %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := modelProviderCommand([]string{"trust", "onnxruntime"}, &stdout, &stderr); code != 0 {
		t.Fatalf("trust .NET guest = %d: %s", code, stderr.String())
	}
	inputBytes := []byte{0, 0, 0, 64, 0, 0, 64, 64}
	input := writeTensorInput(t, []mlTensorInput{{Shape: []int{1, 2}, Format: "float32", Data: inputBytes}})
	stdout.Reset()
	stderr.Reset()
	code := modelCommand([]string{"run", "--provider", "onnxruntime", modelRoot, "onnxruntime/identity@v1", input}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf(".NET guest call = %d, %q", code, stderr.String())
	}
	var result struct {
		Tensors []ml.Tensor `json:"tensors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || len(result.Tensors) != 1 ||
		!bytes.Equal(result.Tensors[0].Data, inputBytes) {
		t.Fatalf("ONNX identity output = %q, %v", stdout.String(), err)
	}
}

func onnxIdentityModel() []byte {
	varint := func(value uint64) []byte {
		var out []byte
		for value >= 0x80 {
			out = append(out, byte(value)|0x80)
			value >>= 7
		}
		return append(out, byte(value))
	}
	number := func(field int, value uint64) []byte {
		return append(varint(uint64(field)<<3), varint(value)...)
	}
	field := func(field int, value []byte) []byte {
		out := append(varint(uint64(field)<<3|2), varint(uint64(len(value)))...)
		return append(out, value...)
	}
	valueInfo := func(name string) []byte {
		shape := append(field(1, number(1, 1)), field(1, number(1, 2))...)
		tensor := append(number(1, 1), field(2, shape)...)
		return append(field(1, []byte(name)), field(2, field(1, tensor))...)
	}
	node := append(field(1, []byte("x")), field(2, []byte("y"))...)
	node = append(node, field(4, []byte("Identity"))...)
	graph := append(field(1, node), field(2, []byte("identity"))...)
	graph = append(graph, field(11, valueInfo("x"))...)
	graph = append(graph, field(12, valueInfo("y"))...)
	model := append(number(1, 8), field(2, []byte("ext-test"))...)
	model = append(model, field(7, graph)...)
	return append(model, field(8, number(2, 13))...)
}

func TestModelCommandRunMissingInput(t *testing.T) {
	root := modelStoreTestHelper(t)
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"run", "--provider", "onnxruntime", root, "onnxruntime/example-model@v1.2.3", "/nonexistent.bin"}, &stdout, &stderr); code == 0 {
		t.Fatal("model run with missing input should fail")
	}
}

func TestModelCommandInstall(t *testing.T) {
	root := t.TempDir()
	manifestFile := filepath.Join(t.TempDir(), "manifest.json")
	manifest := ml.Manifest{
		APIVersion:   ml.ManifestAPIVersion,
		Name:         "test-model",
		Version:      "v1.0.0",
		Provider:     "test-provider",
		Capabilities: []string{"test"},
		Artifacts: []ml.Artifact{
			{Role: "model", File: "model.bin", SHA256: sha256Hex("test data")},
		},
	}
	manifestData, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestFile, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	artifactsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(artifactsDir, "model.bin"), []byte("test data"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"install", root, manifestFile, artifactsDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("model install = %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "test-provider/test-model@v1.0.0") {
		t.Fatalf("install output = %q", stdout.String())
	}
}

func TestModelCommandRemove(t *testing.T) {
	root := modelStoreTestHelper(t)
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"remove", root, "onnxruntime/example-model@v1.2.3"}, &stdout, &stderr); code != 0 {
		t.Fatalf("model remove = %d: %s", code, stderr.String())
	}
	if _, err := ml.OpenStore(root); err != nil {
		t.Fatal(err)
	}
	// Verify it's gone.
	store, _ := ml.OpenStore(root)
	if _, err := store.Resolve(context.Background(), "onnxruntime", "example-model", "v1.2.3"); err == nil {
		t.Fatal("removed model still found")
	}
}

func TestModelCommandVerify(t *testing.T) {
	root := modelStoreTestHelper(t)
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"verify", root, "onnxruntime/example-model@v1.2.3"}, &stdout, &stderr); code != 0 {
		t.Fatalf("model verify = %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "verified") {
		t.Fatalf("verify output = %q", stdout.String())
	}
}

func TestModelCommandUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{}, &stdout, &stderr); code != 2 {
		t.Fatalf("model with no args = %d", code)
	}
	if code := modelCommand([]string{"bogus"}, &stdout, &stderr); code != 2 {
		t.Fatalf("model with bogus action = %d", code)
	}
}

func writeTensorInput(t *testing.T, tensors []mlTensorInput) string {
	t.Helper()
	input := map[string]interface{}{"tensors": tensors}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
