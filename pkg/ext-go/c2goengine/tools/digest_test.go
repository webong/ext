package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripComments(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"no comment", "int x = 1;\n", "int x = 1;\n"},
		{"block", "a /* b */ c", "a   c"},
		{"block between tokens is one space", "a/**/b", "a b"},
		{"block over lines", "a /* one\n two\n*/ b", "a   b"},
		{"line comment keeps the newline", "a // b\nc\n", "a  \nc\n"},
		{"slashes in a string", `s = "http://x/*y*/";` + "\n", `s = "http://x/*y*/";` + "\n"},
		{"comment opener in a char literal", "c = '/'; d = '*'; /* gone */ e;\n", "c = '/'; d = '*';   e;\n"},
		{"quote in a char literal", `c = '"'; /* x */ d = "a";` + "\n", `c = '"';   d = "a";` + "\n"},
		{"escaped quote in a string", `s = "a\"//b"; // gone` + "\n", `s = "a\"//b";  ` + "\n"},
		{"escaped backslash ends the string", `s = "a\\"; // gone` + "\n", `s = "a\\";  ` + "\n"},
		{"line comment continues over a backslash newline", "a // b \\\nstill the comment\nc\n", "a  \nc\n"},
		{"same with a carriage return", "a // b \\\r\nstill the comment\r\nc\r\n", "a  \nc\r\n"},
		{"block opener inside a line comment", "a // /* b\nc */ d\n", "a  \nc */ d\n"},
		{"line opener inside a block comment", "a /* // b */ c\n", "a   c\n"},
		{"unterminated block comment drops the rest", "a /* b\nc\n", "a  "},
		{"a lone apostrophe ends at the line", "#error don't\nint x; /* c */\n", "#error don't\nint x;  \n"},
		{"division is not a comment", "a / b;\n", "a / b;\n"},
	} {
		if got := string(stripComments([]byte(c.in))); got != c.want {
			t.Errorf("%s: stripComments(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func digestOf(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d, err := inputsDigest(dir, []string{"src/a.c"})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDigestIgnoresCommentsAndDocs(t *testing.T) {
	base := map[string]string{
		"src/a.c":        "#include \"a.h\"\n/* the first version */\nint f(void) { return 1; } // one\n",
		"src/a.h":        "// see pkg/plugin-go/engine\nint f(void);\n",
		"vendor/LICENSE": "MIT",
	}
	want := digestOf(t, base)
	if want != digestOf(t, base) {
		t.Fatal("the digest is not deterministic")
	}
	same := map[string]string{
		"src/a.c":          "#include \"a.h\"\n/* reworded,\n   and longer */\nint f(void) { return 1; }\n",
		"src/a.h":          "// see pkg/ext-go/engine\nint f(void);\n",
		"vendor/LICENSE":   "Apache-2.0",
		"vendor/README.md": "docs that nobody compiles",
	}
	if got := digestOf(t, same); got != want {
		t.Errorf("comment and doc edits changed the digest: %s != %s", got, want)
	}
	// A source the translator is not given is not one of its inputs: the store and trust code is
	// built into the C engine and never translated.
	untranslated := map[string]string{"src/a.c": base["src/a.c"], "src/a.h": base["src/a.h"], "src/store.c": "int s;\n"}
	if got := digestOf(t, untranslated); got != want {
		t.Errorf("a source that is not listed changed the digest: %s != %s", got, want)
	}
	for name, changed := range map[string]map[string]string{
		"code":                        {"src/a.c": "#include \"a.h\"\nint f(void) { return 2; }\n", "src/a.h": base["src/a.h"]},
		"header":                      {"src/a.c": base["src/a.c"], "src/a.h": "int f(int);\n"},
		"string":                      {"src/a.c": "#include \"a.h\"\nconst char *u = \"//x\";\nint f(void) { return 1; }\n", "src/a.h": base["src/a.h"]},
		"another header":              {"src/a.c": base["src/a.c"], "src/a.h": base["src/a.h"], "src/b.h": "int g;\n"},
		"preprocessor":                {"src/a.c": "#include \"a.h\"\n#define N 2\nint f(void) { return 1; }\n", "src/a.h": base["src/a.h"]},
		"code swallowed by a comment": {"src/a.c": "#include \"a.h\"\nint f(void) { /* return 1; }\n", "src/a.h": base["src/a.h"]},
	} {
		if got := digestOf(t, changed); got == want {
			t.Errorf("a %s edit did not change the digest", name)
		}
	}
}

// A listed source that is not in the copied tree is an error, not a smaller digest: the generator
// would otherwise name inputs it did not read.
func TestDigestNeedsEveryListedSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "a.c"), []byte("int a;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := inputsDigest(dir, []string{"src/a.c", "src/gone.c"}); err == nil || !strings.Contains(err.Error(), "src/gone.c") {
		t.Fatalf("inputsDigest with a missing listed source = %v, want an error naming src/gone.c", err)
	}
}

func TestCanonicalSource(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"trailing space and tabs", "a;  \t\nb;\n", "a;\nb;\n"},
		{"blank lines go", "a;\n\n\n  \nb;\n", "a;\nb;\n"},
		{"a line of comment goes", "a;\n// all comment\nb;\n", "a;\nb;\n"},
		{"a comment after code leaves the code", "a; /* c */\nb;\n", "a;\nb;\n"},
		{"a blank line after a backslash stays", "#define X 1 \\\n\nint y;\n", "#define X 1 \\\n\nint y;\n"},
		{"a backslash with trailing space still continues", "#define X 1 \\ \n\nint y;\n", "#define X 1 \\\n\nint y;\n"},
		{"spacing inside a line is kept", "s = \"a  b\";\n", "s = \"a  b\";\n"},
		{"indentation is kept", "  a;\n", "  a;\n"},
		{"carriage returns", "a;\r\n\r\nb;\r\n", "a;\nb;\n"},
	} {
		if got := string(canonicalSource([]byte(c.in))); got != c.want {
			t.Errorf("%s: canonicalSource(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
