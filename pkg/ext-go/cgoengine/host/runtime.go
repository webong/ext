package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/webong/ext/pkg/ext-go/cgoengine/host/store"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/process"
)

// RuntimeOptions binds a source plugin to a separately installed, reviewed
// runtime package. The runtime executable can be a file artifact or a file
// inside a verified tree artifact. ScriptArtifact belongs to the plugin
// package, so neither path is selected from ambient PATH.
type RuntimeOptions struct {
	Runtime         store.Installed
	ReviewedDigest  string
	RuntimeArtifact string
	Executable      string
	ScriptArtifact  string
	Arguments       []string
	Options         Options
}

// OpenRuntime re-verifies both installed packages and the reviewed runtime
// digest immediately before starting a source guest. The caller still owns
// plugin-package trust, runtime trust, and operation authorization.
func OpenRuntime(ctx context.Context, installed store.Installed, runtime RuntimeOptions) (*Process, error) {
	if runtime.ReviewedDigest == "" || runtime.RuntimeArtifact == "" || runtime.ScriptArtifact == "" || runtime.Options.Launch != nil {
		return nil, plugin.ErrInvalid
	}
	if runtime.Executable != "" && !plugin.PortablePath(runtime.Executable) {
		return nil, plugin.ErrInvalid
	}
	if err := verifyRuntime(runtime); err != nil {
		return nil, err
	}
	base, err := runtime.Runtime.ArtifactPath(runtime.RuntimeArtifact)
	if err != nil {
		return nil, err
	}
	path := base
	if runtime.Executable != "" {
		path = filepath.Join(base, filepath.FromSlash(runtime.Executable))
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: runtime executable is not a regular file", plugin.ErrInvalid)
	}
	if goruntime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("%w: runtime file is not executable", plugin.ErrInvalid)
	}
	script, err := installed.ArtifactPath(runtime.ScriptArtifact)
	if err != nil {
		return nil, err
	}
	info, err = os.Lstat(script)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: plugin script is not a regular file", plugin.ErrInvalid)
	}
	options := runtime.Options
	previousVerify := options.Verify
	options.Verify = func(ctx context.Context) error {
		if err := verifyRuntime(runtime); err != nil {
			return err
		}
		if previousVerify != nil {
			return previousVerify(ctx)
		}
		return nil
	}
	options.Launch = &process.Command{
		Path: path,
		Args: append([]string{script}, runtime.Arguments...),
		Dir:  installed.Directory,
	}
	return Open(ctx, installed, options)
}

func verifyRuntime(runtime RuntimeOptions) error {
	digest, err := runtime.Runtime.Digest()
	if err != nil {
		return err
	}
	if digest != runtime.ReviewedDigest {
		return fmt.Errorf("%w: runtime package digest changed", plugin.ErrDenied)
	}
	return nil
}
