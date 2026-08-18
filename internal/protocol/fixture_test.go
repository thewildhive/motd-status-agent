package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConsumerFixtureIsValidV1Status(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "v1", "status.json"))
	if err != nil {
		t.Fatal(err)
	}
	var response StatusResponse
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStatus(response); err != nil {
		t.Fatalf("consumer fixture is invalid: %v", err)
	}
	if len(response.Workloads) != 5 {
		t.Fatalf("expected five fixture workloads, got %d", len(response.Workloads))
	}
}
