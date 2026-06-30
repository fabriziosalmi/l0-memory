package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
)

// StartRESTServer runs a lightweight local HTTP server for l0-memory.
// It listens on 127.0.0.1 to ensure it remains strictly local and airgapped.
func StartRESTServer(store *Store, port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	mux := http.NewServeMux()

	// Register routes with Go 1.22+ pattern matching
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /memories", handleGetOrSearchMemories(store))
	mux.HandleFunc("GET /memories/{key}", handleGetMemoryByKey(store))
	mux.HandleFunc("POST /memories", handleSaveMemory(store))
	mux.HandleFunc("DELETE /memories/{key}", handleDeleteMemory(store))

	// CORS wrapper middleware
	handler := corsMiddleware(mux)

	debugf("REST server listening on http://%s", addr)
	fmt.Printf("REST server listening on http://%s\n", addr)
	return http.Serve(listener, handler)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow local origins (e.g. browser extensions, localhost apps)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-LTM-Scope")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func handleGetOrSearchMemories(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ctx := r.Context()

		// Get scope from query parameter or header (X-LTM-Scope), default to user
		scope := r.URL.Query().Get("scope")
		if scope == "" {
			scope = r.Header.Get("X-LTM-Scope")
		}

		limit := 200
		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
				limit = l
			}
		}

		query := r.URL.Query().Get("q")
		if query != "" {
			// Perform FTS & Vector Search (expanded output)
			hits, err := store.SearchExpanded(ctx, scope, query, limit)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error": "%s"}`, err.Error()), http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(hits)
			return
		}

		// Otherwise list memories
		memories, err := store.List(ctx, scope, limit)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": "%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(memories)
	}
}

func handleGetMemoryByKey(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ctx := r.Context()

		key := r.PathValue("key")
		if key == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"key parameter is required"}`))
			return
		}

		scope := r.URL.Query().Get("scope")
		if scope == "" {
			scope = r.Header.Get("X-LTM-Scope")
		}

		m, err := store.Get(ctx, scope, key)
		if err != nil {
			if err == ErrNotFound {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"memory not found"}`))
				return
			}
			http.Error(w, fmt.Sprintf(`{"error": "%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(m)
	}
}

type saveRequest struct {
	Scope       string `json:"scope"`
	Key         string `json:"key"`
	Value       string `json:"value"`
	Tags        string `json:"tags"`
	Origin      string `json:"origin"`
	OriginAgent string `json:"origin_agent"`
}

func handleSaveMemory(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ctx := r.Context()

		var req saveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid JSON request body"}`))
			return
		}

		if req.Key == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"key is required"}`))
			return
		}
		if req.Value == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"value is required"}`))
			return
		}

		opts := &SaveOptions{
			Origin:      req.Origin,
			OriginAgent: req.OriginAgent,
		}

		m, err := store.SaveWithOptions(ctx, req.Scope, req.Key, req.Value, req.Tags, opts)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": "%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(m)
	}
}

func handleDeleteMemory(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ctx := r.Context()

		key := r.PathValue("key")
		if key == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"key parameter is required"}`))
			return
		}

		scope := r.URL.Query().Get("scope")
		if scope == "" {
			scope = r.Header.Get("X-LTM-Scope")
		}

		deleted, err := store.Delete(ctx, scope, key)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": "%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{"deleted": deleted})
	}
}
