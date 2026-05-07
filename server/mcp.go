package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// protocolVersion is the MCP version we declare; clients negotiate compatibility.
const protocolVersion = "2025-06-18"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type mcpServer struct {
	store *Store
	out   io.Writer
	mu    sync.Mutex
	ctx   context.Context
}

func runMCP(ctx context.Context, store *Store, in io.Reader, out io.Writer) error {
	s := &mcpServer{store: store, out: out, ctx: ctx}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			s.writeError(nil, -32700, "parse error: "+err.Error())
			continue
		}
		s.handle(&req)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (s *mcpServer) write(v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	b = append(b, '\n')
	_, _ = s.out.Write(b)
}

func (s *mcpServer) writeError(id json.RawMessage, code int, msg string) {
	s.write(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (s *mcpServer) writeResult(id json.RawMessage, result any) {
	s.write(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *mcpServer) handle(req *rpcRequest) {
	isNotification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		s.writeResult(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "l0-memory",
				"version": Version,
			},
		})
	case "initialized", "notifications/initialized":
		// no-op
	case "tools/list":
		s.writeResult(req.ID, map[string]any{"tools": toolDefs()})
	case "tools/call":
		s.handleToolCall(req)
	case "ping":
		s.writeResult(req.ID, map[string]any{})
	default:
		if !isNotification {
			s.writeError(req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

func toolDefs() []map[string]any {
	return []map[string]any{
		{
			"name":        "memory_save",
			"description": "Save or update a long-term memory entry by (scope, key). Memories are partitioned by scope: \"user\" (default, cross-project) or any string like \"repo:l0-memory\".",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope": map[string]any{"type": "string", "description": "Namespace for this memory. Defaults to \"user\"."},
					"key":   map[string]any{"type": "string", "description": "Identifier within the scope."},
					"value": map[string]any{"type": "string", "description": "Memory content."},
					"tags":  map[string]any{"type": "string", "description": "Optional comma-separated tags."},
				},
				"required": []string{"key", "value"},
			},
		},
		{
			"name":        "memory_get",
			"description": "Retrieve a memory entry by (scope, key). Compact descriptor by default (no value); pass expand:true for the full record. For JSON-valued memories, prefer memory_query to read specific slices.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope":  map[string]any{"type": "string", "description": "Defaults to \"user\"."},
					"key":    map[string]any{"type": "string"},
					"expand": map[string]any{"type": "boolean", "description": "Return the full record including the value field. Default false."},
				},
				"required": []string{"key"},
			},
		},
		{
			"name":        "memory_search",
			"description": "Full-text search (FTS5, BM25-ranked). Compact hits with <<...>>-highlighted snippet by default. Pass scope to restrict the search to one namespace; omit it to search every scope. Pass expand:true for full Memory records.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope":  map[string]any{"type": "string", "description": "Restrict to a single scope. Empty or omitted = all scopes."},
					"query":  map[string]any{"type": "string"},
					"limit":  map[string]any{"type": "integer", "description": "Max results (default 50)."},
					"expand": map[string]any{"type": "boolean", "description": "Return full Memory records instead of compact hits. Default false."},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "memory_list",
			"description": "List memories sorted by pinned DESC, updated_at DESC. Pass scope to restrict; omit it to list every scope.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope": map[string]any{"type": "string", "description": "Restrict to a single scope. Empty or omitted = all scopes."},
					"limit": map[string]any{"type": "integer", "description": "Max results (default 200)."},
				},
			},
		},
		{
			"name":        "memory_delete",
			"description": "Delete a memory entry by (scope, key).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope": map[string]any{"type": "string", "description": "Defaults to \"user\"."},
					"key":   map[string]any{"type": "string"},
				},
				"required": []string{"key"},
			},
		},
		{
			"name":        "memory_query",
			"description": "Slice a JSON-valued memory by JSON Pointer (RFC 6901) path with '*' wildcard segments. Use for graph-shaped or large-blob memories so you don't have to parse the whole value.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope": map[string]any{"type": "string", "description": "Defaults to \"user\"."},
					"key":   map[string]any{"type": "string", "description": "Memory key whose value is JSON."},
					"path":  map[string]any{"type": "string", "description": "JSON Pointer path. Empty or '/' returns the whole document. Use '*' to fan out: e.g. '/repos/*/n'."},
				},
				"required": []string{"key", "path"},
			},
		},
	}
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *mcpServer) handleToolCall(req *rpcRequest) {
	var p toolCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.writeError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}
	result, err := s.dispatchTool(p.Name, p.Arguments)
	if err != nil {
		s.writeResult(req.ID, map[string]any{
			"isError": true,
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
		})
		return
	}
	text, _ := json.MarshalIndent(result, "", "  ")
	s.writeResult(req.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(text)}},
	})
}

func (s *mcpServer) dispatchTool(name string, args json.RawMessage) (any, error) {
	ctx := s.ctx
	switch name {
	case "memory_save":
		var a struct {
			Scope string `json:"scope"`
			Key   string `json:"key"`
			Value string `json:"value"`
			Tags  string `json:"tags"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if strings.TrimSpace(a.Key) == "" {
			return nil, errors.New("'key' is required")
		}
		return s.store.Save(ctx, a.Scope, a.Key, a.Value, a.Tags)
	case "memory_get":
		var a struct {
			Scope  string `json:"scope"`
			Key    string `json:"key"`
			Expand bool   `json:"expand"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if strings.TrimSpace(a.Key) == "" {
			return nil, errors.New("'key' is required")
		}
		m, err := s.store.Get(ctx, a.Scope, a.Key)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return map[string]any{"found": false}, nil
			}
			return nil, err
		}
		if a.Expand {
			return m, nil
		}
		return CompactView(m), nil
	case "memory_search":
		var a struct {
			Scope  string `json:"scope"`
			Query  string `json:"query"`
			Limit  int    `json:"limit"`
			Expand bool   `json:"expand"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if strings.TrimSpace(a.Query) == "" {
			return nil, errors.New("'query' is required")
		}
		if a.Expand {
			return s.store.SearchExpanded(ctx, a.Scope, a.Query, a.Limit)
		}
		return s.store.Search(ctx, a.Scope, a.Query, a.Limit)
	case "memory_list":
		var a struct {
			Scope string `json:"scope"`
			Limit int    `json:"limit"`
		}
		_ = json.Unmarshal(args, &a)
		return s.store.List(ctx, a.Scope, a.Limit)
	case "memory_delete":
		var a struct {
			Scope string `json:"scope"`
			Key   string `json:"key"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if strings.TrimSpace(a.Key) == "" {
			return nil, errors.New("'key' is required")
		}
		ok, err := s.store.Delete(ctx, a.Scope, a.Key)
		if err != nil {
			return nil, err
		}
		return map[string]any{"deleted": ok}, nil
	case "memory_query":
		var a struct {
			Scope string `json:"scope"`
			Key   string `json:"key"`
			Path  string `json:"path"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if strings.TrimSpace(a.Key) == "" {
			return nil, errors.New("'key' is required")
		}
		m, err := s.store.Get(ctx, a.Scope, a.Key)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return map[string]any{"found": false}, nil
			}
			return nil, err
		}
		val, err := EvalJSONPath(m.Value, a.Path)
		if err != nil {
			return nil, err
		}
		return val, nil
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}
