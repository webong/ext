package app

import (
	"fmt"
	"io"
	"strings"
)

// installerCommand edits the installers the user described in installers.json:
// ctx program installer add NAME PATTERN..., remove NAME, ls.
func installerCommand(args []string, stdout, stderr io.Writer) int {
	usage := func() int {
		fmt.Fprintln(stderr, "ctx: program installer needs add <name> <pattern>..., remove <name> or ls")
		return 2
	}
	if len(args) == 0 {
		return usage()
	}
	file, err := loadInstallerFile()
	if err != nil {
		return reportError(stderr, err)
	}
	switch args[0] {
	case "ls":
		if len(args) != 1 {
			return usage()
		}
		for _, item := range file.Installers {
			fmt.Fprintf(stdout, "%s\t%s\n", item.Name, strings.Join(item.Roots, "\t"))
		}
		return 0
	case "add":
		if len(args) < 3 {
			return usage()
		}
		item := configuredInstaller{Name: args[1]}
		index := -1
		for i, existing := range file.Installers {
			if existing.Name == item.Name {
				index, item.Roots = i, append([]string(nil), existing.Roots...)
			}
		}
		added := 0
		for _, root := range args[2:] {
			present := false
			for _, existing := range item.Roots {
				present = present || existing == root
			}
			if !present {
				item.Roots = append(item.Roots, root)
				added++
			}
		}
		if err := validateConfiguredInstaller(item); err != nil {
			return reportErrorCode(stderr, err, 2)
		}
		if index >= 0 {
			file.Installers[index] = item
		} else {
			if len(file.Installers) >= maxConfiguredInstallers {
				return reportErrorCode(stderr, fmt.Errorf("at most %d installers", maxConfiguredInstallers), 2)
			}
			file.Installers = append(file.Installers, item)
		}
		if err := saveInstallerFile(file); err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "installer %s now looks in %d director%s\n", item.Name, len(item.Roots), map[bool]string{true: "y", false: "ies"}[len(item.Roots) == 1])
		return 0
	case "remove":
		if len(args) != 2 {
			return usage()
		}
		var kept []configuredInstaller
		for _, item := range file.Installers {
			if item.Name != args[1] {
				kept = append(kept, item)
			}
		}
		if len(kept) == len(file.Installers) {
			fmt.Fprintf(stderr, "ctx: no configured installer %s\n", args[1])
			return 1
		}
		file.Installers = kept
		if err := saveInstallerFile(file); err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "removed installer %s\n", args[1])
		return 0
	}
	return usage()
}
