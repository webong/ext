// Package testdefinitions supplies service-manager tests with definition state.
// Production definitions are provided by the C ledger binding.
package testdefinitions

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/webong/ext/res/system/service/user"
)

type Backend struct{}

func (Backend) Render(_ context.Context, spec user.Spec, platform user.Platform) (user.Definition, error) {
	if spec.Name == "" || spec.Executable == "" {
		return user.Definition{}, user.ErrInvalid
	}
	name := spec.Name
	suffix := ".plist"
	if platform == user.PlatformLinux {
		suffix = ".service"
		if !strings.HasSuffix(name, suffix) {
			name += suffix
		}
	} else if platform == user.PlatformWindows {
		suffix = ".xml"
	}
	content := fmt.Sprintf("%s\n%s\n", name, spec.Executable)
	fileName := name + suffix
	if platform == user.PlatformLinux {
		fileName = name
	}
	return user.Definition{Platform: platform, Identity: name, FileName: fileName,
		Content: content, Digest: fmt.Sprintf("%x", sha256.Sum256([]byte(content)))}, nil
}

func (Backend) Publish(_ context.Context, def user.Definition, directory string) (user.Publication, error) {
	path := filepath.Join(directory, def.FileName)
	publication := user.Publication{Path: path}
	if bytes, err := os.ReadFile(path); err == nil {
		publication.Existing = true
		if string(bytes) != def.Content {
			return publication, user.ErrExists
		}
		return publication, nil
	}
	if err := os.WriteFile(path, []byte(def.Content), 0o600); err != nil {
		return publication, err
	}
	publication.Created = true
	return publication, nil
}

func (Backend) Inspect(_ context.Context, def user.Definition, directory string) (user.Inspection, error) {
	path := filepath.Join(directory, def.FileName)
	state := user.Inspection{Path: path}
	bytes, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	state.Installed = true
	state.Matches = string(bytes) == def.Content
	state.Digest = fmt.Sprintf("%x", sha256.Sum256(bytes))
	return state, nil
}

func (Backend) Remove(_ context.Context, def user.Definition, directory string) error {
	err := os.Remove(filepath.Join(directory, def.FileName))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

var _ user.Definitions = Backend{}
