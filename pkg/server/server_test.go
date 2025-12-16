package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestServerJSON tests the server with a JSON file
func TestServerJSON(t *testing.T) {
	// Create a temporary JSON file with test data
	tmpDir := t.TempDir()
	jsonFile := filepath.Join(tmpDir, "test.json")
	
	testData := map[string]interface{}{
		"101": map[string]interface{}{
			"Code":      "101",
			"Name":      "Test Record 1",
			"ANBAR":     []interface{}{0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			"Dates":     "00.01.01",
			"FOROSH":    0,
			"Invahed":   1,
		},
		"102": map[string]interface{}{
			"Code":      "102",
			"Name":      "Test Record 2",
			"ANBAR":     []interface{}{0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			"Dates":     "00.01.01",
			"FOROSH":    0,
			"Invahed":   1,
		},
	}
	
	data, err := json.MarshalIndent(testData, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal test data: %v", err)
	}
	
	if err := os.WriteFile(jsonFile, data, 0644); err != nil {
		t.Fatalf("Failed to write test JSON file: %v", err)
	}

	// Create server
	srv, err := NewServer(jsonFile, nil)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer srv.Close()

	// Test GET /api/records
	t.Run("GET /api/records", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/records", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		var response map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if success, ok := response["success"].(bool); !ok || !success {
			t.Error("Expected success=true")
		}

		if count, ok := response["count"].(float64); !ok || count != 2 {
			t.Errorf("Expected count=2, got %v", response["count"])
		}
	})

	// Test GET / (welcome page)
	t.Run("GET /", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("Expected Content-Type text/html; charset=utf-8, got %s", ct)
		}
	})

	// Test GET /viewer
	t.Run("GET /viewer", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/viewer", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("Expected Content-Type text/html; charset=utf-8, got %s", ct)
		}
	})
}

// TestWebSocketUpdates tests WebSocket broadcasting of changes
func TestWebSocketUpdates(t *testing.T) {
	// Create a temporary JSON file
	tmpDir := t.TempDir()
	jsonFile := filepath.Join(tmpDir, "test.json")
	
	testData := map[string]interface{}{
		"101": map[string]interface{}{
			"Code":   "101",
			"Name":   "Original",
			"ANBAR":  []interface{}{0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			"Dates":  "00.01.01",
		},
	}
	
	writeJSON := func(data map[string]interface{}) {
		jsonData, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			t.Fatalf("Failed to marshal JSON: %v", err)
		}
		if err := os.WriteFile(jsonFile, jsonData, 0644); err != nil {
			t.Fatalf("Failed to write JSON file: %v", err)
		}
	}
	
	writeJSON(testData)

	// Create server with file watching
	srv, err := NewServer(jsonFile, nil)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer srv.Close()

	// Start file watching with 0 debounce for tests
	if err := srv.StartWatching(0); err != nil {
		t.Fatalf("Failed to start watching: %v", err)
	}

	// Start test HTTP server
	testServer := httptest.NewServer(srv.router)
	defer testServer.Close()

	// Connect WebSocket client
	wsURL := "ws" + testServer.URL[4:] + "/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect WebSocket: %v", err)
	}
	defer ws.Close()

	// Read initial message
	var initialMsg map[string]interface{}
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	
	if err := ws.ReadJSON(&initialMsg); err != nil {
		t.Fatalf("Failed to read initial message: %v", err)
	}

	if initialMsg["type"] != "initial" {
		t.Errorf("Expected type=initial, got %v", initialMsg["type"])
	}
	
	// Verify initial data has 1 record
	if records, ok := initialMsg["records"].(map[string]interface{}); ok {
		if len(records) != 1 {
			t.Errorf("Expected 1 initial record, got %d", len(records))
		}
	}
	
	// Give file watcher time to settle after initial file creation
	time.Sleep(200 * time.Millisecond)

	// Now modify the JSON file (add a new record)
	testData["102"] = map[string]interface{}{
		"Code":   "102",
		"Name":   "New Record",
		"ANBAR":  []interface{}{0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		"Dates":  "00.01.01",
	}
	writeJSON(testData)

	// Wait for file watcher to detect change and broadcast
	time.Sleep(500 * time.Millisecond)

	// Read update message (may get multiple due to file watcher debounce, skip empty ones)
	var updateMsg map[string]interface{}
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if err := ws.ReadJSON(&updateMsg); err != nil {
			t.Fatalf("Failed to read update message: %v", err)
		}
		// Skip empty updates
		if updateMsg["added"] != nil {
			break
		}
	}

	if updateMsg["type"] != "update" {
		t.Errorf("Expected type=update, got %v", updateMsg["type"])
	}

	// Check for added records
	if added, ok := updateMsg["added"].([]interface{}); ok {
		if len(added) != 1 {
			t.Errorf("Expected 1 added record, got %d", len(added))
		}
	} else {
		t.Error("Expected added field in update message")
	}

	// Delete a record
	delete(testData, "101")
	writeJSON(testData)

	// Wait for file watcher to detect change
	time.Sleep(500 * time.Millisecond)

	// Read delete message (may get multiple due to file watcher debounce, skip empty ones)
	var deleteMsg map[string]interface{}
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if err := ws.ReadJSON(&deleteMsg); err != nil {
			t.Fatalf("Failed to read delete message: %v", err)
		}
		// Skip empty updates
		if deleteMsg["deleted"] != nil {
			break
		}
	}

	if deleteMsg["type"] != "update" {
		t.Errorf("Expected type=update, got %v", deleteMsg["type"])
	}

	// Check for deleted records
	if deleted, ok := deleteMsg["deleted"].([]interface{}); ok {
		if len(deleted) != 1 {
			t.Errorf("Expected 1 deleted record, got %d", len(deleted))
		}
		if deleted[0] != "101" {
			t.Errorf("Expected deleted code=101, got %v", deleted[0])
		}
	} else {
		t.Error("Expected deleted field in update message")
	}
}

// TestComputeChanges tests the change detection logic
func TestComputeChanges(t *testing.T) {
	srv := &Server{}

	// Test case 1: No previous records (all new)
	newRecords := []map[string]interface{}{
		{"Code": "101", "Name": "Record 1"},
		{"Code": "102", "Name": "Record 2"},
	}
	
	changes := srv.computeChanges(newRecords)
	
	if changes["type"] != "update" {
		t.Errorf("Expected type=update, got %v", changes["type"])
	}
	
	if added, ok := changes["added"].([]map[string]interface{}); ok {
		if len(added) != 2 {
			t.Errorf("Expected 2 added records, got %d", len(added))
		}
	} else {
		t.Error("Expected added field")
	}

	// Test case 2: Add a record
	srv.lastRecords = newRecords
	newRecords2 := []map[string]interface{}{
		{"Code": "101", "Name": "Record 1"},
		{"Code": "102", "Name": "Record 2"},
		{"Code": "103", "Name": "Record 3"},
	}
	
	changes = srv.computeChanges(newRecords2)
	
	if added, ok := changes["added"].([]map[string]interface{}); ok {
		if len(added) != 1 {
			t.Errorf("Expected 1 added record, got %d", len(added))
		}
		if added[0]["Code"] != "103" {
			t.Errorf("Expected added code=103, got %v", added[0]["Code"])
		}
	} else {
		t.Error("Expected added field")
	}

	// Test case 3: Delete a record
	srv.lastRecords = newRecords2
	newRecords3 := []map[string]interface{}{
		{"Code": "101", "Name": "Record 1"},
		{"Code": "103", "Name": "Record 3"},
	}
	
	changes = srv.computeChanges(newRecords3)
	
	if deleted, ok := changes["deleted"].([]string); ok {
		if len(deleted) != 1 {
			t.Errorf("Expected 1 deleted record, got %d", len(deleted))
		}
		if deleted[0] != "102" {
			t.Errorf("Expected deleted code=102, got %v", deleted[0])
		}
	} else {
		t.Error("Expected deleted field")
	}
}
