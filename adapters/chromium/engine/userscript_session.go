package chromium

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/webong/ext/res/web/extension"
	"github.com/webong/ext/res/web/userscript"
)

func activateUserscript(ctx context.Context, target DevToolsTarget, record userscript.Record, progress func(extension.InstallResult) error) (map[string]any, error) {
	work, err := os.MkdirTemp("", "ctx-userscript-session-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	source := filepath.Join(work, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		return nil, err
	}
	contentScript := map[string]any{
		"matches": record.Matches,
		"js":      []string{"userscript.js"},
		"run_at":  "document_start",
		"world":   "MAIN",
	}
	if len(record.ExcludeMatches) > 0 {
		contentScript["exclude_matches"] = record.ExcludeMatches
	}
	manifest := map[string]any{
		"manifest_version": 3,
		"name":             "CTX session userscript " + record.ID,
		"version":          "1.0.0",
		"content_scripts":  []any{contentScript},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(source, "manifest.json"), data, 0600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(source, "userscript.js"), []byte(record.Source), 0600); err != nil {
		return nil, err
	}
	revision, err := extension.Inspect(source)
	if err != nil {
		return nil, err
	}
	staged := filepath.Join(work, "staged")
	var activated extension.InstallResult
	err = RunWithDevTools(ctx, target, source, staged, revision.Revision, func(result extension.InstallResult) error {
		activated = result
		return progress(result)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"userscript": record.Description(), "session": activated}, nil
}
