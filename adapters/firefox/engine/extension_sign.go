package firefox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/webong/ext/res/browser/extension"
)

// SignFirefoxXPI uses the caller's web-ext and AMO API credentials. Mozilla
// signs the XPI; the credentials are read from web-ext's environment variables
// and are never passed in process arguments or returned to adapter callers.
func SignFirefoxXPI(ctx context.Context, source, output, revision string) (extension.BuildResult, error) {
	description, err := extension.CheckedSource(source, revision)
	if err != nil {
		return extension.BuildResult{}, err
	}
	if err := extension.OutsideSource(source, output); err != nil {
		return extension.BuildResult{}, err
	}
	if err := extension.UnusedArtifact(output, ".xpi"); err != nil {
		return extension.BuildResult{}, err
	}
	if os.Getenv("WEB_EXT_API_KEY") == "" || os.Getenv("WEB_EXT_API_SECRET") == "" {
		return extension.BuildResult{}, errors.New("Firefox signing requires WEB_EXT_API_KEY and WEB_EXT_API_SECRET for the caller's Mozilla account")
	}
	tool, err := exec.LookPath("web-ext")
	if err != nil {
		return extension.BuildResult{}, errors.New("Firefox signing requires web-ext on PATH")
	}
	work, err := os.MkdirTemp("", "ctx-firefox-sign-")
	if err != nil {
		return extension.BuildResult{}, err
	}
	defer os.RemoveAll(work)
	staged := filepath.Join(work, "extension")
	if _, err := extension.StageWithRevision(source, staged, revision); err != nil {
		return extension.BuildResult{}, err
	}
	artifacts := filepath.Join(work, "artifacts")
	if err := os.Mkdir(artifacts, 0700); err != nil {
		return extension.BuildResult{}, err
	}
	command := exec.CommandContext(ctx, tool, "sign", "--no-input", "--no-config-discovery", "--channel=unlisted", "--source-dir="+staged, "--artifacts-dir="+artifacts)
	command.Dir = work
	if _, err := command.CombinedOutput(); err != nil {
		return extension.BuildResult{}, fmt.Errorf("Mozilla signing failed: %w; check the extension ID, account permissions, and AMO submission status", err)
	}
	entries, err := os.ReadDir(artifacts)
	if err != nil {
		return extension.BuildResult{}, err
	}
	var signed string
	for _, entry := range entries {
		if strings.EqualFold(filepath.Ext(entry.Name()), ".xpi") && entry.Type().IsRegular() {
			if signed != "" {
				return extension.BuildResult{}, errors.New("web-ext produced multiple XPIs")
			}
			signed = filepath.Join(artifacts, entry.Name())
		}
	}
	if signed == "" {
		return extension.BuildResult{}, errors.New("web-ext did not produce a signed XPI")
	}
	if current, err := extension.Inspect(source); err != nil || current.Revision != revision {
		return extension.BuildResult{}, errors.New("extension changed during Mozilla signing")
	}
	if err := extension.CopyArtifactExclusive(signed, output, 0644); err != nil {
		return extension.BuildResult{}, err
	}
	signedDescription, err := extension.Inspect(output)
	if err != nil {
		_ = os.Remove(output)
		return extension.BuildResult{}, fmt.Errorf("signed XPI inspection failed: %w", err)
	}
	return extension.BuildResult{Status: "signed", Browser: "firefox", Source: output, SourceRevision: description.Revision,
		ArtifactRevision: signedDescription.Revision,
		NextAction:       "Use the signed XPI and artifactRevision with extension install for the selected Firefox profile."}, nil
}
