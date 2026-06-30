package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestMux(store *Store) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /memories", handleGetOrSearchMemories(store))
	mux.HandleFunc("GET /memories/{key}", handleGetMemoryByKey(store))
	mux.HandleFunc("POST /memories", handleSaveMemory(store))
	mux.HandleFunc("DELETE /memories/{key}", handleDeleteMemory(store))
	return mux
}

func TestRESTHealth(t *testing.T) {
	mux := newTestMux(nil)

	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	expected := `{"status":"ok"}`
	if rec.Body.String() != expected {
		t.Errorf("expected body %q, got %q", expected, rec.Body.String())
	}
}

func TestRESTSaveAndGetAndDelete(t *testing.T) {
	store := newStore(t)
	mux := newTestMux(store)

	// 1. Save a memory
	savePayload := saveRequest{
		Scope:       "user",
		Key:         "test-key",
		Value:       "test-value",
		Tags:        "test-tag",
		Origin:      "test-origin",
		OriginAgent: "test-agent",
	}
	body, _ := json.Marshal(savePayload)
	req := httptest.NewRequest("POST", "/memories", bytes.NewBuffer(body))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201 Created, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	var savedMemory Memory
	if err := json.Unmarshal(rec.Body.Bytes(), &savedMemory); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if savedMemory.Key != "test-key" || savedMemory.Value != "test-value" {
		t.Errorf("saved memory mismatched: %+v", savedMemory)
	}

	// 2. Get the memory by key
	req = httptest.NewRequest("GET", "/memories/test-key", nil)
	rec = httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d", rec.Code)
	}

	var fetchedMemory Memory
	if err := json.Unmarshal(rec.Body.Bytes(), &fetchedMemory); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if fetchedMemory.Key != "test-key" || fetchedMemory.Value != "test-value" {
		t.Errorf("fetched memory mismatched: %+v", fetchedMemory)
	}

	// 3. Search memories
	req = httptest.NewRequest("GET", "/memories?q=value", nil)
	rec = httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d", rec.Code)
	}

	var hits []SearchHit
	if err := json.Unmarshal(rec.Body.Bytes(), &hits); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(hits) != 1 || hits[0].Key != "test-key" {
		t.Errorf("search results mismatched: %+v", hits)
	}

	// 4. List memories
	req = httptest.NewRequest("GET", "/memories", nil)
	rec = httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d", rec.Code)
	}

	var list []Memory
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(list) != 1 || list[0].Key != "test-key" {
		t.Errorf("list results mismatched: %+v", list)
	}

	// 5. Delete the memory
	req = httptest.NewRequest("DELETE", "/memories/test-key", nil)
	rec = httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d", rec.Code)
	}

	var deleteResponse map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &deleteResponse); err != nil {
		t.Fatalf("failed to decode delete response: %v", err)
	}

	if deleteResponse["deleted"] != true {
		t.Errorf("expected deleted to be true, got %+v", deleteResponse)
	}

	// 6. Verify it is gone
	req = httptest.NewRequest("GET", "/memories/test-key", nil)
	rec = httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404 Not Found, got %d", rec.Code)
	}
}
