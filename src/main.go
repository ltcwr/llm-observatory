package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Chat struct {
	Prompt             string  `json:"prompt" binding:"required"`
	Model              string  `json:"model,omitempty"`
	Answer             string  `json:"answer,omitempty"`
	TotalDuration      int64   `json:"total_duration_ns,omitempty"`
	LoadDuration       int64   `json:"load_duration_ns,omitempty"`
	PromptEvalCount    int     `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64   `json:"prompt_eval_duration_ns,omitempty"`
	EvalCount          int     `json:"eval_count,omitempty"`
	EvalDuration       int64   `json:"eval_duration_ns,omitempty"`
	TokensPerSec       float64 `json:"tokens_per_sec,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Response struct {
	Model              string    `json:"model"`
	CreatedAt          time.Time `json:"created_at"`
	Message            Message   `json:"message"`
	Done               bool      `json:"done"`
	TotalDuration      int64     `json:"total_duration"`
	LoadDuration       int64     `json:"load_duration"`
	PromptEvalCount    int       `json:"prompt_eval_count"`
	PromptEvalDuration int64     `json:"prompt_eval_duration"`
	EvalCount          int       `json:"eval_count"`
	EvalDuration       int64     `json:"eval_duration"`
}

type OllamaRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

var (
	llmRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llm_requests_total",
			Help: "Total number of LLM API requests",
		},
		[]string{"endpoint", "model", "status"},
	)

	llmErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llm_errors_total",
			Help: "Total number of LLM API errors by type",
		},
		[]string{"endpoint", "model", "type"},
	)

	llmRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "llm_request_duration_seconds",
			Help:    "Duration of LLM API requests",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"endpoint", "model", "status"},
	)

	llmGeneratedTokensTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llm_generated_tokens_total",
			Help: "Total number of generated tokens returned by the model",
		},
		[]string{"endpoint", "model"},
	)

	llmPromptTokensTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llm_prompt_tokens_total",
			Help: "Total number of prompt tokens evaluated by the model",
		},
		[]string{"endpoint", "model"},
	)

	llmGenerationSpeed = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "llm_generation_speed_tokens_per_second",
			Help:    "Token generation speed reported by the model",
			Buckets: []float64{1, 5, 10, 20, 50, 100, 200, 500},
		},
		[]string{"endpoint", "model"},
	)

	llmModelTotalDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "llm_model_total_duration_seconds",
			Help:    "Total model processing duration reported by Ollama",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"endpoint", "model"},
	)

	llmModelLoadDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "llm_model_load_duration_seconds",
			Help:    "Model load duration reported by Ollama",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"endpoint", "model"},
	)

	llmModelEvalDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "llm_model_eval_duration_seconds",
			Help:    "Model evaluation duration reported by Ollama",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"endpoint", "model"},
	)
)

func getEnv(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}

func getEnvInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}

	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

var (
	defaultModel    = getEnv("MODEL", "qwen2.5:0.5b")
	ollamaChatURL   = getEnv("OLLAMA_URL", "http://localhost:11434/api/chat")
	ollamaHealthURL = getEnv("OLLAMA_HEALTH_URL", deriveOllamaHealthURL(ollamaChatURL))
	httpClient      = &http.Client{Timeout: time.Duration(getEnvInt("OLLAMA_TIMEOUT_SECONDS", 60)) * time.Second}
)

func deriveOllamaHealthURL(chatURL string) string {
	parsed, err := url.Parse(chatURL)
	if err != nil {
		return "http://localhost:11434/api/tags"
	}

	parsed.Path = "/api/tags"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func helloworld(c *gin.Context) {
	c.String(
		http.StatusOK,
		"Hello World! Time : %s",
		time.Now().Format(time.RFC3339Nano),
	)
}

func healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
	})
}

func readyz(c *gin.Context) {
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, ollamaHealthURL, nil)
	if err != nil {
		slog.Error("failed to create readiness request", "error", err.Error(), "ollama_health_url", ollamaHealthURL)
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  "failed to prepare readiness check",
		})
		return
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		slog.Error("readiness check failed", "error", err.Error(), "ollama_health_url", ollamaHealthURL)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not_ready",
			"error":  "ollama unreachable",
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("readiness check returned non-200", "status_code", resp.StatusCode, "ollama_health_url", ollamaHealthURL)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":              "not_ready",
			"ollama_status_code":  resp.StatusCode,
			"ollama_health_check": ollamaHealthURL,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":              "ready",
		"ollama_health_check": ollamaHealthURL,
	})
}

func handlePrompt(c *gin.Context) {
	const endpoint = "/chat"

	var p Chat
	requestID := c.GetString("request_id")
	start := time.Now()
	statusLabel := "client_error"
	model := defaultModel

	defer func() {
		llmRequestsTotal.WithLabelValues(endpoint, model, statusLabel).Inc()
		llmRequestDuration.WithLabelValues(endpoint, model, statusLabel).Observe(time.Since(start).Seconds())
	}()

	if err := c.ShouldBindJSON(&p); err != nil {
		incrementErrorMetric(endpoint, model, "invalid_request")
		slog.Error("invalid request body", "request_id", requestID, "error", err.Error())
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	if strings.TrimSpace(p.Model) != "" {
		model = strings.TrimSpace(p.Model)
	}

	slog.Info(
		"chat request",
		"request_id", requestID,
		"model", model,
		"prompt_length", len(p.Prompt),
	)

	reqBody := OllamaRequest{
		Model: model,
		Messages: []Message{
			{
				Role:    "user",
				Content: p.Prompt,
			},
		},
		Stream: false,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		statusLabel = "internal_error"
		incrementErrorMetric(endpoint, model, "marshal_request")
		slog.Error("failed to encode ollama request", "request_id", requestID, "model", model, "error", err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to encode upstream request",
		})
		return
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, ollamaChatURL, bytes.NewBuffer(body))
	if err != nil {
		statusLabel = "internal_error"
		incrementErrorMetric(endpoint, model, "build_upstream_request")
		slog.Error("failed to create ollama request", "request_id", requestID, "model", model, "error", err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to prepare upstream request",
		})
		return
	}
	req.Header.Set("Content-Type", "application/json")

	slog.Info(
		"ollama request",
		"request_id", requestID,
		"model", model,
		"ollama_url", ollamaChatURL,
	)

	resp, err := httpClient.Do(req)
	if err != nil {
		statusLabel = "upstream_error"
		incrementErrorMetric(endpoint, model, "upstream_unreachable")
		slog.Error("ollama request failed", "request_id", requestID, "model", model, "error", err.Error())
		c.JSON(http.StatusBadGateway, gin.H{
			"error": "ollama request failed",
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		statusLabel = "upstream_error"
		incrementErrorMetric(endpoint, model, fmt.Sprintf("upstream_status_%d", resp.StatusCode))

		bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if readErr != nil {
			slog.Error("failed to read upstream error body", "request_id", requestID, "model", model, "error", readErr.Error())
		}

		slog.Error(
			"ollama returned non-200",
			"request_id", requestID,
			"model", model,
			"status_code", resp.StatusCode,
			"body", strings.TrimSpace(string(bodyBytes)),
		)

		c.JSON(http.StatusBadGateway, gin.H{
			"error":                "ollama returned non-200 response",
			"ollama_status_code":   resp.StatusCode,
			"ollama_response_body": strings.TrimSpace(string(bodyBytes)),
		})
		return
	}

	var ollamaResp Response
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		statusLabel = "upstream_error"
		incrementErrorMetric(endpoint, model, "decode_upstream_response")
		slog.Error("failed to decode ollama response", "request_id", requestID, "model", model, "error", err.Error())
		c.JSON(http.StatusBadGateway, gin.H{
			"error": "failed to decode ollama response",
		})
		return
	}

	p.Model = model
	p.Answer = ollamaResp.Message.Content
	p.TotalDuration = ollamaResp.TotalDuration
	p.LoadDuration = ollamaResp.LoadDuration
	p.PromptEvalCount = ollamaResp.PromptEvalCount
	p.PromptEvalDuration = ollamaResp.PromptEvalDuration
	p.EvalCount = ollamaResp.EvalCount
	p.EvalDuration = ollamaResp.EvalDuration
	if ollamaResp.EvalDuration > 0 {
		p.TokensPerSec = float64(ollamaResp.EvalCount) * 1e9 / float64(ollamaResp.EvalDuration)
	}

	llmGeneratedTokensTotal.WithLabelValues(endpoint, model).Add(float64(p.EvalCount))
	llmPromptTokensTotal.WithLabelValues(endpoint, model).Add(float64(p.PromptEvalCount))
	if p.TokensPerSec > 0 {
		llmGenerationSpeed.WithLabelValues(endpoint, model).Observe(p.TokensPerSec)
	}
	if p.TotalDuration > 0 {
		llmModelTotalDuration.WithLabelValues(endpoint, model).Observe(float64(p.TotalDuration) / 1e9)
	}
	if p.LoadDuration > 0 {
		llmModelLoadDuration.WithLabelValues(endpoint, model).Observe(float64(p.LoadDuration) / 1e9)
	}
	if p.EvalDuration > 0 {
		llmModelEvalDuration.WithLabelValues(endpoint, model).Observe(float64(p.EvalDuration) / 1e9)
	}

	statusLabel = "success"
	slog.Info(
		"chat response",
		"request_id", requestID,
		"model", model,
		"prompt_eval_count", p.PromptEvalCount,
		"eval_count", p.EvalCount,
		"total_duration_ns", p.TotalDuration,
		"load_duration_ns", p.LoadDuration,
		"eval_duration_ns", p.EvalDuration,
		"tokens_per_sec", p.TokensPerSec,
		"answer_length", len(p.Answer),
	)

	c.JSON(http.StatusOK, p)
}

func incrementErrorMetric(endpoint, model, errorType string) {
	llmErrorsTotal.WithLabelValues(endpoint, model, errorType).Inc()
}

func ginRequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := uuid.NewString()
		c.Set("request_id", requestID)

		start := time.Now()
		c.Next()

		slog.Info(
			"request",
			"request_id", requestID,
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency_ns", time.Since(start).Nanoseconds(),
		)
	}
}

func main() {
	prometheus.MustRegister(
		llmRequestsTotal,
		llmErrorsTotal,
		llmRequestDuration,
		llmGeneratedTokensTotal,
		llmPromptTokensTotal,
		llmGenerationSpeed,
		llmModelTotalDuration,
		llmModelLoadDuration,
		llmModelEvalDuration,
	)

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(ginRequestLogger())

	engine.GET("/", helloworld)
	engine.GET("/healthz", healthz)
	engine.GET("/readyz", readyz)
	engine.POST("/chat", handlePrompt)
	engine.GET("/metrics", gin.WrapH(promhttp.Handler()))

	addr := "0.0.0.0:8080"
	slog.Info("server starting", "addr", addr, "default_model", defaultModel, "ollama_url", ollamaChatURL)
	if err := engine.Run(addr); err != nil {
		slog.Error("server stopped", "error", err.Error())
	}
}
