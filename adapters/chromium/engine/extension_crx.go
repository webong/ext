package chromium

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/webong/ext/res/web/extension"
)

const maxCRXBytes = 64 << 20

// InspectCRX verifies CRX3 proofs, its developer-key identity, and the bounded
// extension archive. Revision is the hash of the complete signed artifact.
// Browser acceptance, publisher trust, and installation are separate checks.
// Format: https://raw.githubusercontent.com/chromium/chromium/main/components/crx_file/crx3.proto
// Algorithms: https://raw.githubusercontent.com/chromium/chromium/main/components/crx_file/crx_verifier.cc
func InspectCRX(source string) (extension.Description, error) {
	data, err := readCRX(source)
	if err != nil {
		return extension.Description{}, err
	}
	return inspectCRX(data)
}

func readCRX(source string) ([]byte, error) {
	if !filepath.IsAbs(source) || !strings.EqualFold(filepath.Ext(source), ".crx") {
		return nil, errors.New("CRX source must be an absolute .crx path")
	}
	info, err := os.Lstat(source)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxCRXBytes {
		return nil, errors.New("CRX source must be a regular file of at most 64 MiB")
	}
	file, err := os.Open(source)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxCRXBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCRXBytes {
		return nil, errors.New("CRX source exceeds 64 MiB")
	}
	return data, nil
}

func inspectCRX(data []byte) (extension.Description, error) {
	if len(data) < 12 || string(data[:4]) != "Cr24" || binary.LittleEndian.Uint32(data[4:8]) != 3 {
		return extension.Description{}, errors.New("a complete CRX3 artifact is required")
	}
	n := uint64(binary.LittleEndian.Uint32(data[8:12]))
	if n == 0 || n > 1<<20 || n+12 >= uint64(len(data)) {
		return extension.Description{}, errors.New("invalid CRX3 header length")
	}
	header := data[12 : 12+n]
	archive := data[12+n:]
	for _, token := range [][]byte{{'P', 'K', 5, 6}, {'P', 'K', 6, 7}, {'P', 'K', 6, 6}} {
		if bytes.Contains(header, token) {
			return extension.Description{}, errors.New("CRX3 header contains an archive terminator")
		}
	}
	fields, err := crxFields(header)
	if err != nil {
		return extension.Description{}, err
	}
	if len(fields[10000]) != 1 {
		return extension.Description{}, errors.New("CRX3 needs one signed header")
	}
	signed := fields[10000][0]
	identity, err := crxFields(signed)
	if err != nil || len(identity[1]) != 1 || len(identity[1][0]) != 16 {
		return extension.Description{}, errors.New("CRX3 has an invalid signed identity")
	}
	idBytes := identity[1][0]
	hash := sha256.New()
	hash.Write([]byte("CRX3 SignedData\x00"))
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], uint32(len(signed)))
	hash.Write(size[:])
	hash.Write(signed)
	hash.Write(archive)
	digest := hash.Sum(nil)
	count := len(fields[2]) + len(fields[3])
	if count == 0 || count > 32 {
		return extension.Description{}, errors.New("CRX3 needs bounded signing proofs")
	}
	developer := false
	for _, algorithm := range []uint64{2, 3} {
		for _, raw := range fields[algorithm] {
			proof, err := crxFields(raw)
			if err != nil || len(proof[1]) != 1 || len(proof[2]) != 1 {
				return extension.Description{}, errors.New("invalid CRX3 signing proof")
			}
			public, signature := proof[1][0], proof[2][0]
			key, err := x509.ParsePKIXPublicKey(public)
			if err != nil {
				return extension.Description{}, errors.New("invalid CRX3 public key")
			}
			valid := false
			if algorithm == 2 {
				if rsaKey, ok := key.(*rsa.PublicKey); ok && rsaKey.N.BitLen() <= 8192 {
					valid = rsa.VerifyPKCS1v15(rsaKey, crypto.SHA256, digest, signature) == nil
				}
			} else {
				if ecKey, ok := key.(*ecdsa.PublicKey); ok && ecKey.Curve == elliptic.P256() {
					valid = ecdsa.VerifyASN1(ecKey, digest, signature)
				}
			}
			if !valid {
				return extension.Description{}, errors.New("CRX3 signature verification failed")
			}
			keyHash := sha256.Sum256(public)
			developer = developer || bytes.Equal(keyHash[:16], idBytes)
		}
	}
	if !developer {
		return extension.Description{}, errors.New("CRX3 signing key does not match its extension identity")
	}
	zipReader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return extension.Description{}, errors.New("CRX3 payload is not a valid ZIP")
	}
	rootManifest := false
	for _, entry := range zipReader.File {
		rootManifest = rootManifest || entry.Name == "manifest.json"
	}
	if !rootManifest {
		return extension.Description{}, errors.New("CRX3 needs manifest.json at the archive root")
	}
	// Reuse portable archive validation without moving CRX interpretation into
	// core or extracting any untrusted entries to the filesystem.
	work, err := os.MkdirTemp("", "ctx-crx-inspect-")
	if err != nil {
		return extension.Description{}, err
	}
	defer os.RemoveAll(work)
	payload := filepath.Join(work, "extension.zip")
	if err := os.WriteFile(payload, archive, 0600); err != nil {
		return extension.Description{}, err
	}
	description, err := extension.Inspect(payload)
	if err != nil {
		return extension.Description{}, err
	}
	if !validCRXVersion(description.Version) {
		return extension.Description{}, errors.New("CRX3 manifest has an invalid Chrome version")
	}
	var id strings.Builder
	for _, value := range idBytes {
		id.WriteByte('a' + (value >> 4))
		id.WriteByte('a' + (value & 15))
	}
	artifactHash := sha256.Sum256(data)
	description.ID = id.String()
	description.Revision = "sha256:" + hex.EncodeToString(artifactHash[:])
	return description, nil
}

// crxFields reads bounded protobuf wire fields without requiring a generated
// parser or interpreting unknown fields. Singular signed/proof fields are
// checked by their consumers; deprecated group fields are rejected.
func crxFields(data []byte) (map[uint64][][]byte, error) {
	fields := map[uint64][][]byte{}
	for len(data) > 0 {
		tag, n := binary.Uvarint(data)
		if n <= 0 || tag>>3 == 0 || tag>>3 > 1<<29-1 {
			return nil, errors.New("invalid CRX3 protobuf field")
		}
		data = data[n:]
		var value []byte
		switch tag & 7 {
		case 0:
			_, n = binary.Uvarint(data)
			if n <= 0 {
				return nil, errors.New("invalid CRX3 protobuf integer")
			}
			data = data[n:]
		case 1:
			if len(data) < 8 {
				return nil, errors.New("truncated CRX3 field")
			}
			data = data[8:]
		case 2:
			size, n := binary.Uvarint(data)
			if n <= 0 || size > uint64(len(data)-n) {
				return nil, errors.New("invalid CRX3 protobuf length")
			}
			data = data[n:]
			value = data[:size]
			data = data[size:]
		case 5:
			if len(data) < 4 {
				return nil, errors.New("truncated CRX3 field")
			}
			data = data[4:]
		default:
			return nil, errors.New("unsupported CRX3 protobuf wire type")
		}
		// Record all occurrences, including wrong wire types, to reject ambiguous
		// signed-header and proof fields rather than silently replacing them.
		fields[tag>>3] = append(fields[tag>>3], value)
		if len(fields[tag>>3]) > 64 || len(fields) > 256 {
			return nil, errors.New("CRX3 has too many header fields")
		}
	}
	return fields, nil
}

func validCRXVersion(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) == 0 || len(parts) > 4 {
		return false
	}
	nonzero := false
	for _, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
		number, err := strconv.ParseUint(part, 10, 16)
		if err != nil {
			return false
		}
		nonzero = nonzero || number != 0
	}
	return nonzero
}

func (backend extensionBackend) InspectExtension(ctx context.Context, source string) (extension.Description, error) {
	if err := ctx.Err(); err != nil {
		return extension.Description{}, err
	}
	if strings.EqualFold(filepath.Ext(source), ".crx") {
		return InspectCRX(source)
	}
	return extension.Inspect(source)
}

func checkedCRX(source, revision, id, version string) (extension.Description, error) {
	if revision == "" {
		return extension.Description{}, errors.New("a prepared CRX artifact revision is required")
	}
	description, err := InspectCRX(source)
	if err != nil {
		return extension.Description{}, err
	}
	if description.Revision != revision {
		return extension.Description{}, errors.New("CRX artifact changed since preparation")
	}
	if id != "" && id != description.ID || version != "" && version != description.Version {
		return extension.Description{}, fmt.Errorf("CRX identity or version differs from the supplied metadata")
	}
	return description, nil
}
