package ctl

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/store"
)

func TestWriteFixtureRoundTripsThroughLoadFixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "happy.json")
	if err := WriteFixture(sampleForest(), path); err != nil {
		t.Fatalf("WriteFixture: %v", err)
	}
	data, _ := os.ReadFile(path)
	tr, err := store.LoadFixture(data)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if len(tr.Roots) != 1 || tr.Roots[0].Name != "invoke_agent researchbot" {
		t.Fatalf("round-trip root wrong: %+v", tr.Roots)
	}
	if len(tr.ByOp(genai.OpExecuteTool)) != 2 {
		t.Fatalf("round-trip tool count wrong")
	}
}
