package main

import (
	"bytes"
	"path/filepath"
	"strings"
)

// isCSource reports whether the file is read by the C translator: the sources and headers. Anything
// else in the copied directories (a README, a license) cannot change the translation, so it is not an
// input of the digest.
func isCSource(path string) bool {
	switch filepath.Ext(path) {
	case ".c", ".h":
		return true
	}
	return false
}

// stripComments returns src with its comments removed, each replaced by one space as translation
// phase 3 of the C standard does. Comment markers inside string and character literals are text, and
// are kept; a literal ends at its closing quote or at the end of the line, so a stray apostrophe
// cannot swallow code that follows. A line comment continues over a backslash-newline, as the standard
// has it. The digest hashes the result, so editing a comment does not make a translation stale.
func stripComments(src []byte) []byte {
	out := make([]byte, 0, len(src))
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '"' || c == '\'':
			start := i
			i++
			for i < len(src) && src[i] != c && src[i] != '\n' {
				if src[i] == '\\' && i+1 < len(src) && src[i+1] != '\n' {
					i++
				}
				i++
			}
			if i < len(src) && src[i] == c {
				i++
			}
			out = append(out, src[start:i]...)
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := bytes.Index(src[i+2:], []byte("*/"))
			if end < 0 {
				i = len(src)
			} else {
				i += 2 + end + 2
			}
			out = append(out, ' ')
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			i += 2
			for i < len(src) && src[i] != '\n' {
				if src[i] == '\\' && i+1 < len(src) && (src[i+1] == '\n' || (src[i+1] == '\r' && i+2 < len(src) && src[i+2] == '\n')) {
					// the comment continues on the next line
					i++
					if src[i] == '\r' {
						i++
					}
				}
				i++
			}
			out = append(out, ' ')
		default:
			out = append(out, c)
			i++
		}
	}
	return out
}

// canonicalSource is what the digest hashes of a C file: the file without its comments, trailing
// whitespace and blank lines. What a comment leaves behind (a space, an empty line) would otherwise
// make adding or removing a comment change the digest. A blank line after a line that ends in a
// backslash stays, because the preprocessor joins it to that line. Spacing inside a line, and so
// inside a string literal, is kept.
func canonicalSource(src []byte) []byte {
	var out strings.Builder
	continued := false
	for _, line := range strings.Split(string(stripComments(src)), "\n") {
		line = strings.TrimRight(line, " \t\r\f\v")
		if line == "" && !continued {
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
		continued = strings.HasSuffix(line, "\\")
	}
	return []byte(out.String())
}
