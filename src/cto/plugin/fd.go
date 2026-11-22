package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// attachFDs preserves explicitly named command descriptors at their original
// numbers in a child. The caller closes the returned filler files after Start.
// A package cannot request arbitrary descriptors; ext supplies the allowed
// flag names from its verified command declaration.
func attachFDs(cmd *exec.Cmd, args, flags []string) ([]*os.File, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	allowed := map[string]bool{}
	for _, flag := range flags {
		allowed[flag] = true
	}
	requested := map[int]bool{}
	for i := 0; i < len(args); i++ {
		flag, value, inline := strings.Cut(args[i], "=")
		if !allowed[flag] {
			continue
		}
		if !inline {
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("%s needs a descriptor number", flag)
			}
			value = args[i]
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 3 || n > 64 {
			return nil, fmt.Errorf("%s needs a descriptor from 3 to 64", flag)
		}
		requested[n] = true
	}
	if len(requested) == 0 {
		return nil, nil
	}
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("inherited numeric descriptors are unavailable on Windows")
	}
	max := 0
	for n := range requested {
		if n > max {
			max = n
		}
	}
	var fillers []*os.File
	for n := 3; n <= max; n++ {
		if requested[n] {
			f, err := duplicateFile(n)
			if err != nil {
				closeFiles(fillers)
				return nil, fmt.Errorf("descriptor %d: %w", n, err)
			}
			fillers = append(fillers, f)
			cmd.ExtraFiles = append(cmd.ExtraFiles, f)
			continue
		}
		f, err := os.Open(os.DevNull)
		if err != nil {
			closeFiles(fillers)
			return nil, err
		}
		fillers = append(fillers, f)
		cmd.ExtraFiles = append(cmd.ExtraFiles, f)
	}
	return fillers, nil
}

func closeFiles(files []*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}
