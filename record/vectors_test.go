package record_test

import (
	"encoding/json"
	"os"
	"testing"
)

func TestVectorFileHasEmptyVectorsArray(t *testing.T) {
	data, err := os.ReadFile("../testdata/vectors.json")
	if err != nil {
		t.Fatalf("read vector file: %v", err)
	}
	var file struct {
		Vectors []json.RawMessage `json:"vectors"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("parse vector file: %v", err)
	}
	if file.Vectors == nil {
		t.Fatal("vectors must be an array, not null or missing")
	}
	if len(file.Vectors) != 0 {
		t.Fatalf("vectors holds %d entries, want 0 until #15", len(file.Vectors))
	}
}
