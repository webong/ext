package arch_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run the real gate against an isolated workspace. Stub only external Go/Git
// operations so unpublished dependencies and successful builds are deterministic.
func TestStandaloneReleaseGate(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is required for the standalone release script")
	}
	script, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts/check-standalone-modules.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, allow, only, published, goFail, want string
		fail                                       bool
	}{
		{"unpublished dependency", "", "", "0", "0", "required standalone modules were skipped", true},
		{"development opt-in", "1", "", "0", "0", "skipped 1, failed 0", false},
		{"selected modules remain required", "1", "pkg/consumer", "0", "0", "required standalone modules were skipped", true},
		{"published dependencies", "0", "pkg/consumer", "1", "0", "checked 1 module(s), skipped 0, failed 0", false},
		{"build failure", "1", "", "1", "1", "FAIL pkg/consumer: go vet", true},
		{"missing required module", "1", "pkg/missing", "1", "0", "missing go.mod", true},
		{"product explicitly required", "1", "cmd/ext", "1", "0", "product modules cannot be checked standalone", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write := func(name, content string, mode os.FileMode) {
				t.Helper()
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), mode); err != nil {
					t.Fatal(err)
				}
			}
			write("scripts/check-standalone-modules.sh", string(script), 0755)
			write("go.work", "use (\n ./pkg/consumer\n)\n", 0644)
			write("pkg/consumer/go.mod", "module example.test/consumer\nrequire github.com/webong/ext/pkg/ext-go/engine v0.1.0\n", 0644)
			write("bin/git", "#!/bin/sh\ncase \"$1\" in\nls-remote) [ \"$TEST_PUBLISHED\" = 1 ] ;;\ntag) exit 0 ;;\n*) exit 2 ;;\nesac\n", 0755)
			write("bin/go", "#!/bin/sh\nif [ \"$TEST_GO_FAIL\" = 1 ]; then exit 1; fi\nif [ \"$1\" = list ]; then echo example.test/consumer; fi\n", 0755)
			cmd := exec.Command(bash, filepath.Join(root, "scripts/check-standalone-modules.sh"))
			// Remove inherited controls so this test also works inside the CI opt-in job.
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				if key == "PATH" || strings.HasPrefix(key, "STANDALONE_") || strings.HasPrefix(key, "TEST_") {
					continue
				}
				cmd.Env = append(cmd.Env, entry)
			}
			cmd.Env = append(cmd.Env, "PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"), "STANDALONE_OS=linux", "STANDALONE_ALLOW_SKIPS="+tc.allow, "STANDALONE_ONLY="+tc.only, "TEST_PUBLISHED="+tc.published, "TEST_GO_FAIL="+tc.goFail)
			out, err := cmd.CombinedOutput()
			if (err != nil) != tc.fail || !strings.Contains(string(out), tc.want) {
				t.Fatalf("error = %v, want failure %v and output containing %q; output:\n%s", err, tc.fail, tc.want, out)
			}
		})
	}
}
