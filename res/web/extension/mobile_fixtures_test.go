package extension

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// These archives are consumed by Go, Swift, JVM and Android device tests.
func TestMobileArtifactFixtures(t *testing.T) {
	root := filepath.Join("..", "testdata", "extension")
	var expected Description
	data, err := os.ReadFile(filepath.Join(root, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		File       string
		Valid      bool
		MobileOnly bool
	}
	data, err = os.ReadFile(filepath.Join(root, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.File, func(t *testing.T) {
			if c.MobileOnly {
				t.Skip("additional mobile ZIP validation")
			}
			got, err := Inspect(filepath.Join(root, c.File))
			if !c.Valid {
				if err == nil {
					t.Fatal("accepted unsafe fixture")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("description: %+v; expected %+v", got, expected)
			}
		})
	}
}
