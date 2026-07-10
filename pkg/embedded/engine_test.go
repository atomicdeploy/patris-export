package embedded

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCallJSONRejectsUnknownMethod(t *testing.T) {
	e := &Engine{}
	raw, err := e.CallJSON(context.Background(), `{"id":1,"method":"missing"}`)
	if err != nil {
		t.Fatalf("CallJSON returned error: %v", err)
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if resp.OK || resp.Error == "" {
		t.Fatalf("expected failed response, got %#v", resp)
	}
}
