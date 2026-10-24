package main

import "testing"

func TestTargetsExpr(t *testing.T) {
	for _, c := range []struct {
		files []string
		want  string
	}{
		{[]string{"generated_darwin_arm64.go"}, "darwin && arm64"},
		{[]string{"x/generated_linux_arm64.go", "x/generated_darwin_arm64.go"}, "(darwin && arm64) || (linux && arm64)"},
		{[]string{"generated_windows_amd64.go", "generated_linux_arm64.go", "generated_darwin_arm64.go"}, "(darwin && arm64) || (linux && arm64) || (windows && amd64)"},
	} {
		expr, err := targetsExpr(c.files)
		if err != nil || expr.String() != c.want {
			t.Errorf("%v: %v, %v; want %s", c.files, expr, err, c.want)
		}
	}
	if _, err := targetsExpr([]string{"call.go", "readme.md"}); err == nil {
		t.Error("no generated file accepted")
	}
}
