package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestChatUsesMockOllama(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/chat" {
			t.Fatalf("expected /api/chat, got %s", r.URL.Path)
		}

		var req OllamaRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "test-model" {
			t.Fatalf("expected model test-model, got %s", req.Model)
		}
		if req.Stream {
			t.Fatal("expected stream=false")
		}
		if len(req.Messages) != 1 || req.Messages[0].Content != "Salut" {
			t.Fatalf("unexpected messages: %+v", req.Messages)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{
			Model: "test-model",
			Message: Message{
				Role:    "assistant",
				Content: "Bonjour depuis le mock",
			},
			Done:               true,
			TotalDuration:      1_000_000_000,
			LoadDuration:       100_000_000,
			PromptEvalCount:    5,
			PromptEvalDuration: 200_000_000,
			EvalCount:          10,
			EvalDuration:       500_000_000,
		})
	}))
	defer ollama.Close()

	oldURL := ollamaChatURL
	oldModel := defaultModel
	ollamaChatURL = ollama.URL + "/api/chat"
	defaultModel = "test-model"
	defer func() {
		ollamaChatURL = oldURL
		defaultModel = oldModel
	}()

	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"prompt":"Salut"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	newRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d with body %s", rec.Code, rec.Body.String())
	}

	var got Chat
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Model != "test-model" {
		t.Fatalf("expected default model, got %s", got.Model)
	}
	if got.Answer != "Bonjour depuis le mock" {
		t.Fatalf("unexpected answer: %s", got.Answer)
	}
	if got.EvalCount != 10 || got.PromptEvalCount != 5 {
		t.Fatalf("unexpected token counts: eval=%d prompt=%d", got.EvalCount, got.PromptEvalCount)
	}
	if got.TokensPerSec != 20 {
		t.Fatalf("expected 20 tokens/sec, got %f", got.TokensPerSec)
	}
}

func TestChatReturnsBadGatewayWhenOllamaFails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusInternalServerError)
	}))
	defer ollama.Close()

	oldURL := ollamaChatURL
	ollamaChatURL = ollama.URL
	defer func() {
		ollamaChatURL = oldURL
	}()

	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"prompt":"Salut","model":"missing-model"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	newRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d with body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ollama returned non-200 response") {
		t.Fatalf("unexpected error body: %s", rec.Body.String())
	}
}

func TestProtectedEndpointsRequireAPIKeyWhenConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)

	oldAPIKey := apiKey
	apiKey = "secret"
	defer func() {
		apiKey = oldAPIKey
	}()

	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"prompt":"Salut"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	newRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d with body %s", rec.Code, rec.Body.String())
	}
}

func TestProtectedEndpointsAcceptAPIKeyHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{
			Model: "test-model",
			Message: Message{
				Role:    "assistant",
				Content: "Bonjour",
			},
			Done: true,
		})
	}))
	defer ollama.Close()

	oldURL := ollamaChatURL
	oldModel := defaultModel
	oldAPIKey := apiKey
	ollamaChatURL = ollama.URL
	defaultModel = "test-model"
	apiKey = "secret"
	defer func() {
		ollamaChatURL = oldURL
		defaultModel = oldModel
		apiKey = oldAPIKey
	}()

	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"prompt":"Salut"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "secret")
	rec := httptest.NewRecorder()

	newRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d with body %s", rec.Code, rec.Body.String())
	}
}

func TestHealthEndpointsStayPublicWhenAPIKeyConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)

	oldAPIKey := apiKey
	apiKey = "secret"
	defer func() {
		apiKey = oldAPIKey
	}()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	newRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d with body %s", rec.Code, rec.Body.String())
	}
}
