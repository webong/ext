package store

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"strings"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
)

// An archive format is one reader: it walks the entries of an archive file and
// hands each to visit. Bounds, path checks and the refusal of links are the
// extraction's, written once; a reader only describes what the archive holds.
// A new format is one more archiveFormat in archiveFormats. Only formats the
// standard library reads are here, since this module imports nothing else.
type archiveFormat struct {
	name string
	// match reports whether an archive starting with head (up to 512 bytes) is
	// in this format.
	match func(head []byte) bool
	// read visits every entry in archive order and stops at the first error.
	read func(source io.ReaderAt, size int64, visit func(archiveEntry) error) error
}

type entryKind int

const (
	// entryOther is a link, a device or anything else that is never extracted.
	entryOther entryKind = iota
	entryDirectory
	entryFile
)

// archiveEntry is one entry as every format describes it.
type archiveEntry struct {
	name string
	kind entryKind
	// size is the declared size of a file's body.
	size int64
	mode fs.FileMode
	// open returns a file's body. It is valid only during the visit.
	open func() (io.ReadCloser, error)
}

var archiveFormats = []archiveFormat{
	{name: "zip", read: readZip, match: func(head []byte) bool {
		return bytes.HasPrefix(head, []byte("PK\x03\x04")) || bytes.HasPrefix(head, []byte("PK\x05\x06"))
	}},
	{name: "tar.gz", read: readTarGzip, match: func(head []byte) bool {
		return bytes.HasPrefix(head, []byte{0x1f, 0x8b})
	}},
	{name: "tar", read: readTar, match: func(head []byte) bool {
		return len(head) >= 262 && string(head[257:262]) == "ustar"
	}},
}

func detectArchive(head []byte) (archiveFormat, error) {
	names := make([]string, 0, len(archiveFormats))
	for _, format := range archiveFormats {
		if format.match(head) {
			return format, nil
		}
		names = append(names, format.name)
	}
	return archiveFormat{}, fmt.Errorf("%w: archive format is not recognized; supported formats are %s", plugin.ErrUnsupported, strings.Join(names, ", "))
}

func readZip(source io.ReaderAt, size int64, visit func(archiveEntry) error) error {
	archive, err := zip.NewReader(source, size)
	if err != nil {
		return fmt.Errorf("%w: reading zip archive: %v", plugin.ErrInvalid, err)
	}
	// The zip64 form is not read, by any store: a package archive has no use for
	// it, and one rule everywhere is worth more than the reach. It shows as a
	// count or a size that does not fit the original fields.
	if size > math.MaxUint32 || len(archive.File) >= math.MaxUint16 {
		return fmt.Errorf("%w: zip64 archives are not supported", plugin.ErrInvalid)
	}
	for _, file := range archive.File {
		entry := archiveEntry{name: file.Name, mode: file.Mode()}
		switch {
		case entry.mode.IsDir() || strings.HasSuffix(file.Name, "/"):
			entry.kind = entryDirectory
		case entry.mode.IsRegular() && (file.CompressedSize == math.MaxUint32 || file.UncompressedSize == math.MaxUint32):
			return fmt.Errorf("%w: zip64 archives are not supported", plugin.ErrInvalid)
		case entry.mode.IsRegular() && file.UncompressedSize64 <= 1<<62:
			entry.kind, entry.size = entryFile, int64(file.UncompressedSize64)
			entry.open = func() (io.ReadCloser, error) {
				body, err := file.Open()
				if err != nil {
					return nil, fmt.Errorf("%w: reading zip archive: %v", plugin.ErrInvalid, err)
				}
				return body, nil
			}
		case entry.mode.IsRegular():
			return fmt.Errorf("%w: archive exceeds its size limits", plugin.ErrInvalid)
		}
		if err := visit(entry); err != nil {
			return err
		}
	}
	return nil
}

func readTarGzip(source io.ReaderAt, size int64, visit func(archiveEntry) error) error {
	compressed, err := gzip.NewReader(io.NewSectionReader(source, 0, size))
	if err != nil {
		return fmt.Errorf("%w: archive is not a gzip-compressed tar", plugin.ErrInvalid)
	}
	defer compressed.Close()
	return readTarStream(compressed, visit)
}

func readTar(source io.ReaderAt, size int64, visit func(archiveEntry) error) error {
	return readTarStream(io.NewSectionReader(source, 0, size), visit)
}

// blockCounter counts what a tar reader consumes.
type blockCounter struct {
	source io.Reader
	read   int64
}

func (c *blockCounter) Read(p []byte) (int, error) {
	n, err := c.source.Read(p)
	c.read += int64(n)
	return n, err
}

func readTarStream(source io.Reader, visit func(archiveEntry) error) error {
	counted := &blockCounter{source: source}
	reader := tar.NewReader(counted)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			// archive/tar takes an archive that stops inside a header block as
			// one that has ended. A tar is whole blocks: anything else was cut.
			if counted.read%512 != 0 {
				return fmt.Errorf("%w: tar archive ends inside a block", plugin.ErrInvalid)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: reading tar archive: %v", plugin.ErrInvalid, err)
		}
		entry := archiveEntry{name: header.Name, mode: header.FileInfo().Mode()}
		switch header.Typeflag {
		case tar.TypeXGlobalHeader:
			continue // metadata for the whole archive (git archive writes one)
		case tar.TypeDir:
			entry.kind = entryDirectory
		case tar.TypeReg:
			entry.kind, entry.size = entryFile, header.Size
			entry.open = func() (io.ReadCloser, error) { return io.NopCloser(reader), nil }
		}
		if err := visit(entry); err != nil {
			return err
		}
	}
}
