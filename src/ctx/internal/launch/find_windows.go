//go:build windows

package launch

import (
	"os"
	"path/filepath"
	"strings"
)

func executableNames(name string) []string {
	if filepath.Ext(name) != "" {
		return []string{name}
	}
	extensions := filepath.SplitList(os.Getenv("PATHEXT"))
	if len(extensions) == 0 {
		extensions = []string{".COM", ".EXE", ".BAT", ".CMD"}
	}
	result := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		result = append(result, name+strings.ToLower(extension))
		result = append(result, name+strings.ToUpper(extension))
	}
	return result
}

func isExecutable(info os.FileInfo) bool { return !info.IsDir() }
