package userscript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Swift and Java consume this exact Go record and the same on-disk layout.
func TestMobileStoreFixture(t *testing.T) {
	data, err := os.ReadFile("../testdata/userscript/store/record.json")
	if err != nil {
		t.Fatal(err)
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if err := Validate(record); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := Save(directory, record, false); err != nil {
		t.Fatal(err)
	}
	stored, err := Load(directory, record.Target, record.ID)
	if err != nil || !reflect.DeepEqual(record, stored) {
		t.Fatalf("load: %v", err)
	}
	path, err := filePath(directory, record.Target, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(filepath.Dir(path)) != "edcb219f80355327e74351977daf7a16144d20773871f784ab3d5c725acc7ad0" {
		t.Fatal("target hash layout drift")
	}
	disabled, err := SetEnabled(directory, record.Target, record.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled || disabled.Revision != record.Revision {
		t.Fatal("enable flag changed review identity")
	}
	if err := Remove(directory, record.Target, record.ID); err != nil {
		t.Fatal(err)
	}
	records, err := List(directory, record.Target)
	if err != nil || len(records) != 0 {
		t.Fatalf("remove: %v", err)
	}
}
