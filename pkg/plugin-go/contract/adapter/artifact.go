package adapter

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const artifactMetadataName = "ctx-package.json"
const maxArtifactBytes int64 = 256 << 20
const maxExpandedBytes int64 = 512 << 20
const maxArtifactEntries = 4096
const maxIndexBytes int64 = 1 << 20

var validTarget = regexp.MustCompile(`^[a-z0-9][a-z0-9_]*$`)

type artifactMetadata struct {
	FormatVersion int    `json:"format_version"`
	Name          string `json:"name"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
}

type artifactIndex struct {
	FormatVersion int                         `json:"format_version"`
	Name          string                      `json:"name"`
	Packages      map[string]artifactLocation `json:"packages"`
}

type artifactLocation struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// PackageDirectory writes a platform-specific adapter archive from a ready-to-run directory.
func PackageDirectory(directory, output, goos, goarch string) error {
	if !validTarget.MatchString(goos) || !validTarget.MatchString(goarch) {
		return errors.New("package target needs an operating system and architecture")
	}
	adapter, err := LoadDirectoryForOS(directory, goos)
	if err != nil {
		return err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	if relative, err := filepath.Rel(adapter.Directory, output); err == nil &&
		(relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return errors.New("package output must be outside the adapter directory")
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(output), ".ctx-adapter-package-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	if err := file.Chmod(0o644); err != nil {
		file.Close()
		os.Remove(temporary)
		return err
	}
	defer func() {
		file.Close()
		os.Remove(temporary)
	}()
	writer := zip.NewWriter(file)
	metadata, err := json.Marshal(artifactMetadata{FormatVersion: 1, Name: adapter.Manifest.Name, OS: goos, Arch: goarch})
	if err != nil {
		return err
	}
	if err := writeArtifactFile(writer, artifactMetadataName, 0o644, append(metadata, '\n')); err != nil {
		return err
	}
	var total int64
	entries := 0
	err = filepath.WalkDir(adapter.Directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported adapter package entry: %s", path)
		}
		relative, err := filepath.Rel(adapter.Directory, path)
		if err != nil {
			return err
		}
		archiveName := filepath.ToSlash(relative)
		if !safeArtifactPath(archiveName) {
			return fmt.Errorf("unsafe adapter package path %q", archiveName)
		}
		if archiveName == artifactMetadataName {
			return fmt.Errorf("adapter directory must not contain %s", artifactMetadataName)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		entries++
		if total > maxExpandedBytes || entries > maxArtifactEntries {
			return errors.New("adapter package exceeds size or file limit")
		}
		contents, err := os.Open(path)
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			contents.Close()
			return err
		}
		header.Name = archiveName
		header.Method = zip.Deflate
		mode := info.Mode()
		if goos != "windows" && header.Name == filepath.ToSlash(adapter.Manifest.Executable) {
			mode = 0o755
		}
		header.SetMode(mode)
		output, err := writer.CreateHeader(header)
		if err != nil {
			contents.Close()
			return err
		}
		_, copyErr := io.Copy(output, contents)
		closeErr := contents.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if info, err := os.Stat(temporary); err != nil || info.Size() > maxArtifactBytes {
		return errors.New("adapter archive exceeds size limit")
	}
	return replaceFile(temporary, output)
}

func writeArtifactFile(writer *zip.Writer, name string, mode fs.FileMode, data []byte) error {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(mode)
	output, err := writer.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = output.Write(data)
	return err
}

// WriteIndex records platform archives and their digests for one adapter.
// Archives must sit beside the index so its references work locally and over HTTPS.
func WriteIndex(output string, packages []string) error {
	if len(packages) == 0 {
		return errors.New("adapter index needs at least one archive")
	}
	output, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	index := artifactIndex{FormatVersion: 1, Packages: map[string]artifactLocation{}}
	seenFiles := map[string]bool{}
	for _, packagePath := range packages {
		archivePath, err := filepath.Abs(packagePath)
		if err != nil {
			return err
		}
		if filepath.Dir(archivePath) != filepath.Dir(output) {
			return errors.New("adapter index and archives must be in the same directory")
		}
		name := filepath.Base(archivePath)
		if !safeArtifactPath(name) || seenFiles[strings.ToLower(name)] || name == filepath.Base(output) {
			return fmt.Errorf("duplicate or invalid adapter archive %s", name)
		}
		seenFiles[strings.ToLower(name)] = true
		metadata, digest, err := inspectArtifact(archivePath)
		if err != nil {
			return err
		}
		if index.Name == "" {
			index.Name = metadata.Name
		} else if index.Name != metadata.Name {
			return errors.New("adapter index cannot mix different adapters")
		}
		key := metadata.OS + "/" + metadata.Arch
		if _, exists := index.Packages[key]; exists {
			return fmt.Errorf("adapter index repeats platform %s", key)
		}
		index.Packages[key] = artifactLocation{URL: name, SHA256: digest}
	}
	contents, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(output), ".ctx-adapter-index-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o644); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(append(contents, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return replaceFile(file.Name(), output)
}

func inspectArtifact(path string) (artifactMetadata, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return artifactMetadata{}, "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return artifactMetadata{}, "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxArtifactBytes {
		return artifactMetadata{}, "", errors.New("adapter archive exceeds size limit or is not a regular file")
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return artifactMetadata{}, "", err
	}
	archive, err := zip.NewReader(file, info.Size())
	if err != nil {
		return artifactMetadata{}, "", err
	}
	for _, entry := range archive.File {
		if entry.Name != artifactMetadataName {
			continue
		}
		if entry.UncompressedSize64 > uint64(maxIndexBytes) {
			return artifactMetadata{}, "", errors.New("adapter package metadata is oversized")
		}
		input, err := entry.Open()
		if err != nil {
			return artifactMetadata{}, "", err
		}
		contents, readErr := io.ReadAll(io.LimitReader(input, maxIndexBytes+1))
		closeErr := input.Close()
		if readErr != nil || closeErr != nil {
			return artifactMetadata{}, "", errors.Join(readErr, closeErr)
		}
		var metadata artifactMetadata
		if len(contents) > int(maxIndexBytes) || json.Unmarshal(contents, &metadata) != nil ||
			metadata.FormatVersion != 1 || !validName.MatchString(metadata.Name) ||
			!validTarget.MatchString(metadata.OS) || !validTarget.MatchString(metadata.Arch) {
			return artifactMetadata{}, "", errors.New("adapter archive has invalid package metadata")
		}
		return metadata, hex.EncodeToString(digest.Sum(nil)), nil
	}
	return artifactMetadata{}, "", fmt.Errorf("adapter archive lacks %s", artifactMetadataName)
}

// InstallSource installs a local adapter directory, a package archive, or an HTTPS archive.
// Remote archives require a caller-supplied SHA-256 digest.
func (s *Store) InstallSource(source, expectedSHA256 string) (*Adapter, error) {
	return s.installSource(source, expectedSHA256, "")
}

func (s *Store) installSource(source, expectedSHA256, expectedName string) (*Adapter, error) {
	if expectedSHA256 != "" {
		if err := validateArtifactSHA(expectedSHA256); err != nil {
			return nil, err
		}
	}
	if strings.HasSuffix(sourcePath(source), ".ctxadapter.json") {
		if expectedName != "" {
			return nil, errors.New("adapter indexes cannot contain another index")
		}
		return s.installIndex(source, expectedSHA256)
	}
	if strings.HasPrefix(source, "http://") {
		return nil, errors.New("adapter packages must be downloaded over HTTPS")
	}
	if strings.HasPrefix(source, "https://") {
		if expectedSHA256 == "" {
			return nil, errors.New("remote adapter package requires --sha256")
		}
		archive, err := downloadArtifact(source, maxArtifactBytes)
		if err != nil {
			return nil, err
		}
		defer os.Remove(archive)
		return s.installArchive(archive, expectedSHA256, expectedName)
	}
	info, err := os.Lstat(source)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		if expectedName != "" {
			return nil, errors.New("adapter index must refer to an archive")
		}
		if expectedSHA256 != "" {
			return nil, errors.New("--sha256 applies to adapter archives, not directories")
		}
		return s.Install(source)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("adapter source must be a directory or regular archive")
	}
	return s.installArchive(source, expectedSHA256, expectedName)
}

func sourcePath(source string) string {
	if strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "http://") {
		parsed, err := url.Parse(source)
		if err == nil {
			return parsed.Path
		}
	}
	return source
}

func (s *Store) installIndex(source, expectedSHA256 string) (*Adapter, error) {
	path := source
	if strings.HasPrefix(source, "http://") {
		return nil, errors.New("adapter indexes must be downloaded over HTTPS")
	}
	if strings.HasPrefix(source, "https://") {
		if expectedSHA256 == "" {
			return nil, errors.New("remote adapter index requires --sha256")
		}
		var err error
		path, err = downloadArtifact(source, maxIndexBytes)
		if err != nil {
			return nil, err
		}
		defer os.Remove(path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxIndexBytes {
		return nil, errors.New("adapter index exceeds size limit or is not a regular file")
	}
	if err := verifyArtifactSHA(path, expectedSHA256); err != nil {
		return nil, err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var index artifactIndex
	if err := json.Unmarshal(contents, &index); err != nil {
		return nil, fmt.Errorf("invalid adapter index: %w", err)
	}
	if index.FormatVersion != 1 || !validName.MatchString(index.Name) || reservedNames[index.Name] {
		return nil, errors.New("adapter index has invalid metadata")
	}
	key := runtime.GOOS + "/" + runtime.GOARCH
	location, ok := index.Packages[key]
	if !ok {
		return nil, fmt.Errorf("adapter %s has no package for %s", index.Name, key)
	}
	if location.URL == "" || location.SHA256 == "" || strings.HasSuffix(sourcePath(location.URL), ".ctxadapter.json") {
		return nil, errors.New("adapter index has an invalid package reference")
	}
	packageSource := location.URL
	if strings.HasPrefix(source, "https://") {
		base, err := url.Parse(source)
		if err != nil {
			return nil, err
		}
		reference, err := url.Parse(location.URL)
		if err != nil {
			return nil, err
		}
		resolved := base.ResolveReference(reference)
		if resolved.Scheme != "https" || resolved.User != nil {
			return nil, errors.New("remote adapter index must reference an HTTPS package")
		}
		packageSource = resolved.String()
	} else if !strings.HasPrefix(location.URL, "https://") {
		if filepath.IsAbs(location.URL) || !validPackagePath(location.URL) {
			return nil, errors.New("local adapter index has an invalid package path")
		}
		packageSource = filepath.Join(filepath.Dir(source), filepath.FromSlash(location.URL))
	}
	return s.installSource(packageSource, location.SHA256, index.Name)
}

func downloadArtifact(source string, limit int64) (string, error) {
	client := &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if request.URL.Scheme != "https" {
				return errors.New("adapter download redirected away from HTTPS")
			}
			if len(via) >= 10 {
				return errors.New("too many adapter download redirects")
			}
			return nil
		},
	}
	response, err := client.Get(source)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("adapter download returned HTTP %d", response.StatusCode)
	}
	file, err := os.CreateTemp("", "ctx-adapter-download-*.ctxadapter")
	if err != nil {
		return "", err
	}
	path := file.Name()
	count, copyErr := io.Copy(file, io.LimitReader(response.Body, limit+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || count > limit {
		os.Remove(path)
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		return "", errors.New("adapter download exceeds size limit")
	}
	return path, nil
}

func (s *Store) installArchive(path, expectedSHA256, expectedName string) (*Adapter, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxArtifactBytes {
		return nil, errors.New("adapter archive exceeds size limit or is not a regular file")
	}
	if err := verifyArtifactSHA(path, expectedSHA256); err != nil {
		return nil, err
	}
	archive, err := zip.NewReader(file, info.Size())
	if err != nil {
		return nil, fmt.Errorf("open adapter archive: %w", err)
	}
	staging, err := os.MkdirTemp("", "ctx-adapter-extract-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	if err := extractArtifact(archive, staging); err != nil {
		return nil, err
	}
	metadataFile, err := os.Open(filepath.Join(staging, artifactMetadataName))
	if err != nil {
		return nil, fmt.Errorf("adapter archive lacks %s", artifactMetadataName)
	}
	var metadata artifactMetadata
	decoder := json.NewDecoder(metadataFile)
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&metadata)
	closeErr := metadataFile.Close()
	if decodeErr != nil || closeErr != nil {
		return nil, errors.New("adapter archive has invalid package metadata")
	}
	if metadata.FormatVersion != 1 || !validName.MatchString(metadata.Name) {
		return nil, errors.New("adapter archive has unsupported package metadata")
	}
	if metadata.OS != runtime.GOOS || metadata.Arch != runtime.GOARCH {
		return nil, fmt.Errorf("adapter package targets %s/%s, this machine is %s/%s", metadata.OS, metadata.Arch, runtime.GOOS, runtime.GOARCH)
	}
	loaded, err := LoadDirectory(staging)
	if err != nil {
		return nil, err
	}
	if loaded.Manifest.Name != metadata.Name {
		return nil, errors.New("adapter package name does not match its manifest")
	}
	if expectedName != "" && loaded.Manifest.Name != expectedName {
		return nil, errors.New("adapter package name does not match its index")
	}
	return s.Install(staging)
}

func verifyArtifactSHA(path, expected string) error {
	if expected == "" {
		return nil
	}
	if err := validateArtifactSHA(expected); err != nil {
		return err
	}
	actual, err := FileSHA256(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(expected, actual) {
		return errors.New("adapter SHA-256 mismatch")
	}
	return nil
}

func validateArtifactSHA(value string) error {
	if len(value) != 64 {
		return errors.New("SHA-256 must contain 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("invalid SHA-256: %w", err)
	}
	return nil
}

// FileSHA256 returns the checksum users can pin when publishing an adapter index.
func FileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func extractArtifact(archive *zip.Reader, target string) error {
	if len(archive.File) == 0 || len(archive.File) > maxArtifactEntries+1 {
		return errors.New("adapter archive has an invalid file count")
	}
	seen := map[string]bool{}
	var total int64
	for _, entry := range archive.File {
		name := strings.TrimSuffix(entry.Name, "/")
		if !safeArtifactPath(name) {
			return fmt.Errorf("unsafe adapter archive path %q", entry.Name)
		}
		key := strings.ToLower(name)
		if seen[key] {
			return fmt.Errorf("duplicate adapter archive path %q", entry.Name)
		}
		seen[key] = true
		destination := filepath.Join(target, filepath.FromSlash(name))
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(destination, 0o700); err != nil {
				return err
			}
			continue
		}
		if !entry.Mode().IsRegular() || entry.UncompressedSize64 > uint64(maxExpandedBytes-total) {
			return fmt.Errorf("invalid or oversized adapter archive entry %q", entry.Name)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		input, err := entry.Open()
		if err != nil {
			return err
		}
		mode := entry.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			input.Close()
			return err
		}
		count, copyErr := io.Copy(output, io.LimitReader(input, maxExpandedBytes-total+1))
		inputErr := input.Close()
		outputErr := output.Close()
		if copyErr != nil || inputErr != nil || outputErr != nil {
			return errors.Join(copyErr, inputErr, outputErr)
		}
		total += count
		if total > maxExpandedBytes {
			return errors.New("adapter archive exceeds expanded size limit")
		}
	}
	return nil
}

func safeArtifactPath(name string) bool {
	if name == "" || !fs.ValidPath(name) || strings.ContainsAny(name, "\\:\x00") {
		return false
	}
	for _, character := range name {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if len(part) > 255 || strings.TrimRight(part, ". ") != part {
			return false
		}
		stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" ||
			len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9' {
			return false
		}
	}
	return true
}
