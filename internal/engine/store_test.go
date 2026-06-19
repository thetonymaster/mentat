package engine

import (
	"testing"

	"github.com/thetonymaster/mentat/internal/config"
)

func TestBuildStoreResolvesTempo(t *testing.T) {
	st, err := BuildStore(config.Config{Store: "tempo", Tempo: config.Endpoint{Endpoint: "http://localhost:3200"}})
	if err != nil {
		t.Fatalf("BuildStore: %v", err)
	}
	if st == nil {
		t.Fatal("BuildStore returned nil store for tempo")
	}
}

func TestBuildStoreUnknownErrors(t *testing.T) {
	_, err := BuildStore(config.Config{Store: "telepathy"})
	if err == nil {
		t.Fatal("want error for unknown store, got nil")
	}
}
