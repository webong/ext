package generate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// inputsMarker starts the line of a generated file that names the digest it was made from.
const inputsMarker = "// c2goengine inputs sha256:"

// inputsDigest hashes the translation's inputs: the listed sources and every header under csrc in a
// fixed order, the flags, the source list, and the versions of ccgo and libc. A source the translator
// is not given (the store and trust code, the Windows host) is not an input, so adding or editing one
// does not make the translations stale; a header is, because any listed source may include it.
func inputsDigest(csrc string, sources []string) (string, error) {
	h := sha256.New()
	fmt.Fprintf(h, "libc %s\nflags %q\nsources %q\n", libcVersion, flags, sources)
	fmt.Fprintf(h, "ccgo %s\n", CompilerVersion)
	// Only the C the translator reads counts, and without its comments (see stripComments): a README,
	// a license or a reworded comment does not change the translation, so it must not turn the
	// workflow red.
	fmt.Fprintf(h, "inputs: the listed sources and every header, comments and blank space stripped\n")
	listed := map[string]bool{}
	for _, s := range sources {
		listed[s] = true
	}
	var files []string
	err := filepath.WalkDir(csrc, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isCSource(path) {
			return err
		}
		rel, err := filepath.Rel(csrc, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if filepath.Ext(rel) == ".c" {
			if !listed[rel] {
				return nil
			}
			delete(listed, rel)
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(listed) > 0 {
		missing := make([]string, 0, len(listed))
		for s := range listed {
			missing = append(missing, s)
		}
		sort.Strings(missing)
		return "", fmt.Errorf("listed sources are not in the copied tree: %s", strings.Join(missing, ", "))
	}
	sort.Strings(files)
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(csrc, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(canonicalSource(data))
		fmt.Fprintf(h, "%s %s\n", rel, hex.EncodeToString(sum[:]))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
