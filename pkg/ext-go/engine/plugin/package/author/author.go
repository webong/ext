// Package author builds and seals packages without selecting a product or contract.
package author

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

const maxBytes int64 = 512 << 20
const maxFiles = 4096

var targetPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_]*$`)

type Options struct{ Source, Output, OS, Arch, ManifestPath string }

// Read accepts an ext.json template. Full validation occurs before any artifact is opened.
func Read(source string) (packagekit.Manifest, error) { return ReadAt(source, "ext.json") }

func ReadAt(source, manifestPath string) (packagekit.Manifest, error) {
	var m packagekit.Manifest
	path, err := (storelayout.Layout{ManifestPath: manifestPath}).Locate(source)
	if err != nil {
		return m, err
	}
	f, err := os.Open(filepath.Join(source, path))
	if err != nil {
		return m, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (16<<20)+1))
	if err != nil {
		return m, err
	}
	if len(data) > 16<<20 {
		return m, fmt.Errorf("manifest too large")
	}
	if err = plugin.Decode(data, &m); err != nil {
		return m, err
	}
	check := m
	check.Artifacts = append([]packagekit.Artifact(nil), m.Artifacts...)
	if check.Descriptor.Identity.Revision == "" {
		check.Descriptor.Identity.Revision = strings.Repeat("0", 64)
	}
	for i := range check.Artifacts {
		if check.Artifacts[i].SHA256 == "" {
			check.Artifacts[i].SHA256 = strings.Repeat("0", 64)
		}
	}
	return m, check.Validate()
}

// Prepare copies only declared artifacts, filters explicit targets and seals a private staging directory.
// Existing hashes and revisions are never silently replaced. The caller removes staging.
func Prepare(o Options) (staging string, m packagekit.Manifest, err error) {
	if !targetPattern.MatchString(o.OS) || !targetPattern.MatchString(o.Arch) {
		err = fmt.Errorf("invalid package target")
		return
	}
	if o.ManifestPath == "" {
		o.ManifestPath = "ext.json"
	}
	m, err = ReadAt(o.Source, o.ManifestPath)
	if err != nil {
		return
	}
	staging, err = os.MkdirTemp("", "ext-package-author-")
	if err != nil {
		return
	}
	defer func() {
		if err != nil {
			os.RemoveAll(staging)
		}
	}()
	kept := map[string]bool{}
	artifacts := make([]packagekit.Artifact, 0, len(m.Artifacts))
	var budget copyBudget
	for _, a := range m.Artifacts {
		if a.OS != "" && a.OS != o.OS || a.Arch != "" && a.Arch != o.Arch {
			continue
		}
		if storelayout.Reserves(o.ManifestPath, a.Path, a.Kind == packagekit.ArtifactTree) {
			err = fmt.Errorf("artifact overlaps manifest")
			return
		}
		if err = budget.copy(o.Source, staging, a.Path); err != nil {
			return
		}
		var digest string
		if a.Kind == packagekit.ArtifactTree {
			digest, err = packagekit.TreeDigest(staging, a.Path)
		} else {
			digest, err = packagekit.FileDigest(filepath.Join(staging, filepath.FromSlash(a.Path)))
		}
		if err != nil {
			return
		}
		if a.SHA256 != "" && a.SHA256 != digest {
			err = fmt.Errorf("artifact %s digest mismatch", a.Name)
			return
		}
		a.SHA256 = digest
		kept[a.Name] = true
		artifacts = append(artifacts, a)
	}
	m.Artifacts = artifacts
	entries := m.Entrypoints[:0]
	for _, e := range m.Entrypoints {
		if kept[e.Artifact] {
			entries = append(entries, e)
		}
	}
	m.Entrypoints = entries
	assets := m.Assets[:0]
	for _, a := range m.Assets {
		if kept[a.Artifact] {
			assets = append(assets, a)
		}
	}
	m.Assets = assets
	if m.Descriptor.Identity.Revision == "" {
		// Executable guests commonly report their own executable hash at handshake.
		if len(m.Entrypoints) == 1 {
			a, _ := m.Artifact(m.Entrypoints[0].Artifact)
			m.Descriptor.Identity.Revision = a.SHA256
		} else {
			data, _ := json.Marshal(m)
			sum := sha256.Sum256(data)
			m.Descriptor.Identity.Revision = hex.EncodeToString(sum[:])
		}
	}
	err = store.WriteManifestAt(staging, o.ManifestPath, m)
	if err == nil {
		err = m.VerifyArtifacts(staging)
	}
	return
}

type copyBudget struct {
	bytes int64
	files int
}

func (b *copyBudget) copy(source, dest, relative string) error {
	// Check each ancestor: WalkDir alone would follow a symlink in the root path.
	current := source
	info, err := os.Lstat(current)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("source must be a real directory")
	}
	for _, part := range strings.Split(filepath.ToSlash(relative), "/") {
		if part == "" || part == ".." {
			return fmt.Errorf("unsafe artifact path")
		}
		current = filepath.Join(current, part)
		info, err = os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is not a package artifact: %s", relative)
		}
	}
	return filepath.WalkDir(current, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular package artifact: %s", rel)
		}
		b.files++
		if b.files > maxFiles {
			return fmt.Errorf("too many package files")
		}
		if err = os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		opened, err := input.Stat()
		if err != nil {
			return err
		}
		if !os.SameFile(info, opened) {
			return fmt.Errorf("artifact changed while copying")
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		limit := maxBytes - b.bytes
		if limit > store.DefaultMaxArtifactBytes {
			limit = store.DefaultMaxArtifactBytes
		}
		n, copyErr := io.Copy(output, io.LimitReader(input, limit+1))
		b.bytes += n
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if b.bytes > maxBytes || n > limit {
			return fmt.Errorf("package exceeds size limit")
		}
		return nil
	})
}

// Pack writes a new ZIP. It never overwrites existing output or installs/trusts a package.
func Pack(o Options) error {
	stage, _, err := Prepare(o)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if o.Output == "" {
		return fmt.Errorf("package output is required")
	}
	if _, err = os.Lstat(o.Output); err == nil {
		return fmt.Errorf("output already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(o.Output), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(o.Output), ".ext-package-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	z := zip.NewWriter(f)
	err = filepath.WalkDir(stage, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(stage, path)
		header.Name = filepath.ToSlash(rel)
		header.Method = zip.Deflate
		out, err := z.CreateHeader(header)
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(out, in)
		return err
	})
	ze := z.Close()
	fe := f.Close()
	if err != nil {
		return err
	}
	if ze != nil {
		return ze
	}
	if fe != nil {
		return fe
	}
	// Link publishes atomically without replacing a concurrent writer's output.
	return os.Link(f.Name(), o.Output)
}

// Build runs the author's explicit command in an isolated source copy, then packs its declared outputs.
// The command is executed directly, never interpreted by an implicit shell.
func Build(ctx context.Context, o Options, command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("build requires an explicit command after --")
	}
	if o.ManifestPath == "" {
		o.ManifestPath = "ext.json"
	}
	if _, err := ReadAt(o.Source, o.ManifestPath); err != nil {
		return err
	}
	stage, err := os.MkdirTemp("", "ext-package-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	var budget copyBudget
	if err = budget.copy(o.Source, stage, "."); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = stage
	env := os.Environ()
	for _, key := range []string{"GOWORK", "GOOS", "GOARCH", "EXT_PACKAGE_OS", "EXT_PACKAGE_ARCH"} {
		filtered := env[:0]
		for _, v := range env {
			if !strings.HasPrefix(v, key+"=") {
				filtered = append(filtered, v)
			}
		}
		env = filtered
	}
	cmd.Env = append(env, "GOWORK=off", "GOOS="+o.OS, "GOARCH="+o.Arch, "EXT_PACKAGE_OS="+o.OS, "EXT_PACKAGE_ARCH="+o.Arch)
	var output buildOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("package build: %w\n%s", err, output.String())
	}
	o.Source = stage
	return Pack(o)
}

// Build logs are bounded even when a tool emits unbounded output.
type buildOutput struct{ bytes.Buffer }

func (b *buildOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (1 << 20) - b.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}
