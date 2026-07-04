package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// StartRESTServer runs a lightweight local HTTP server for l0-memory.
// It listens on 127.0.0.1 to ensure it remains strictly local and airgapped,
// and gates every route (except GET /health) behind a bearer token — because
// 127.0.0.1 binding alone does not stop a malicious web page from calling it.
func StartRESTServer(store *Store, port int) error {
	token, source, err := loadServeToken()
	if err != nil {
		return fmt.Errorf("failed to load serve token: %w", err)
	}

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

	// Token gate innermost, CORS outermost so even 401s carry CORS headers
	// (the browser extension needs to read the error body).
	handler := corsMiddleware(requireToken(token, mux))

	debugf("REST server listening on http://%s (token source: %s)", addr, source)
	fmt.Printf("REST server listening on http://%s\n", addr)
	fmt.Printf("Auth token: %s\n", token)
	fmt.Printf("  (source: %s — paste it into the Web Clipper extension)\n", source)
	return http.Serve(listener, handler)
}

// requireToken rejects any request that does not present the serve token via
// `X-LTM-Token: <token>` or `Authorization: Bearer <token>`. GET /health and
// CORS preflight (OPTIONS) are exempt so liveness checks and the browser
// preflight still work without credentials.
func requireToken(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		provided := r.Header.Get("X-LTM-Token")
		if provided == "" {
			if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
				provided = strings.TrimSpace(strings.TrimPrefix(a, "Bearer "))
			}
		}
		if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"missing or invalid token — set X-LTM-Token (see the token printed by ` + "`ltm serve`" + `)"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// corsMiddleware reflects the request Origin only for browser-extension schemes
// (chrome-extension:// / moz-extension://). Arbitrary web pages get no
// Access-Control-Allow-Origin, so even with the token the browser blocks them
// from reading responses — defense-in-depth behind requireToken. Non-browser
// clients (curl, the CLI) send no Origin and are unaffected.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if strings.HasPrefix(origin, "chrome-extension://") || strings.HasPrefix(origin, "moz-extension://") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-LTM-Scope, X-LTM-Token, Authorization")

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
