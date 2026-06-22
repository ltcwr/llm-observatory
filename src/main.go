package main

import (
	"bytes"
	"context"
	"crypto/subtle"
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
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
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

const (
	defaultRequestBodyLimitBytes = 1 << 20
	defaultOllamaTimeoutSeconds  = 60
)

type Config struct {
	DefaultModel          string
	OllamaChatURL         string
	OllamaHealthURL       string
	APIKey                string
	RequestBodyLimitBytes int64
	OTELServiceName       string
	OTELEndpoint          string
}

type Server struct {
	config     Config
	httpClient *http.Client
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

func getEnvInt64(key string, fallback int64) int64 {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}

	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func loadConfig() Config {
	ollamaChatURL := getEnv("OLLAMA_URL", "http://localhost:11434/api/chat")
	return Config{
		DefaultModel:          getEnv("MODEL", "qwen2.5:0.5b"),
		OllamaChatURL:         ollamaChatURL,
		OllamaHealthURL:       getEnv("OLLAMA_HEALTH_URL", deriveOllamaHealthURL(ollamaChatURL)),
		APIKey:                strings.TrimSpace(getEnv("API_KEY", "")),
		RequestBodyLimitBytes: getEnvInt64("REQUEST_BODY_LIMIT_BYTES", defaultRequestBodyLimitBytes),
		OTELServiceName:       getEnv("OTEL_SERVICE_NAME", "llm-observatory-api"),
		OTELEndpoint:          getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4318"),
	}
}

func newHTTPClient(timeoutSeconds int) *http.Client {
	return &http.Client{
		Timeout:   time.Duration(timeoutSeconds) * time.Second,
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}
}

func newServer(config Config, client *http.Client) *Server {
	if config.RequestBodyLimitBytes <= 0 {
		config.RequestBodyLimitBytes = defaultRequestBodyLimitBytes
	}
	if client == nil {
		client = newHTTPClient(defaultOllamaTimeoutSeconds)
	}

	return &Server{
		config:     config,
		httpClient: client,
	}
}

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

func (s *Server) readyz(c *gin.Context) {
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, s.config.OllamaHealthURL, nil)
	if err != nil {
		slog.Error("failed to create readiness request", "error", err.Error(), "ollama_health_url", s.config.OllamaHealthURL)
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  "failed to prepare readiness check",
		})
		return
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		slog.Error("readiness check failed", "error", err.Error(), "ollama_health_url", s.config.OllamaHealthURL)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not_ready",
			"error":  "ollama unreachable",
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("readiness check returned non-200", "status_code", resp.StatusCode, "ollama_health_url", s.config.OllamaHealthURL)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":              "not_ready",
			"ollama_status_code":  resp.StatusCode,
			"ollama_health_check": s.config.OllamaHealthURL,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":              "ready",
		"ollama_health_check": s.config.OllamaHealthURL,
	})
}

func (s *Server) handlePrompt(c *gin.Context) {
	const endpoint = "/chat"

	var p Chat
	requestID := c.GetString("request_id")
	traceID := c.GetString("trace_id")
	start := time.Now()
	statusLabel := "client_error"
	model := s.config.DefaultModel

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

	p.Prompt = strings.TrimSpace(p.Prompt)
	if p.Prompt == "" {
		incrementErrorMetric(endpoint, model, "empty_prompt")
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "prompt must not be empty",
		})
		return
	}

	if strings.TrimSpace(p.Model) != "" {
		model = strings.TrimSpace(p.Model)
	}

	slog.Info(
		"chat request",
		"request_id", requestID,
		"trace_id", traceID,
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

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, s.config.OllamaChatURL, bytes.NewBuffer(body))
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
		"trace_id", traceID,
		"model", model,
		"ollama_url", s.config.OllamaChatURL,
	)

	resp, err := s.httpClient.Do(req)
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
			"error":              "ollama returned non-200 response",
			"ollama_status_code": resp.StatusCode,
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
		"trace_id", traceID,
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

func currentTraceID(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}

func ginRequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := uuid.NewString()
		traceID := currentTraceID(c.Request.Context())
		c.Set("request_id", requestID)
		c.Set("trace_id", traceID)

		start := time.Now()
		c.Next()

		slog.Info(
			"request",
			"request_id", requestID,
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency_ns", time.Since(start).Nanoseconds(),
			"trace_id", traceID,
		)
	}
}

func (s *Server) apiKeyAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.config.APIKey == "" {
			c.Next()
			return
		}

		token := strings.TrimSpace(c.GetHeader("X-API-Key"))
		if token == "" {
			token = strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
			token = strings.TrimSpace(token)
		}

		if subtle.ConstantTimeCompare([]byte(token), []byte(s.config.APIKey)) != 1 {
			slog.Warn(
				"unauthorized request",
				"request_id", c.GetString("request_id"),
				"path", c.Request.URL.Path,
			)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing or invalid API key",
			})
			return
		}

		c.Next()
	}
}

func requestBodyLimit(limitBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limitBytes)
		}
		c.Next()
	}
}

func initTracer(ctx context.Context, config Config) (func(context.Context) error, error) {
	exporter, err := otlptracehttp.New(
		ctx,
		otlptracehttp.WithEndpoint(config.OTELEndpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			"",
			attribute.String("service.name", config.OTELServiceName),
		),
	)
	if err != nil {
		return nil, err
	}

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tracerProvider.Shutdown, nil
}

func newRouter(server *Server) *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(otelgin.Middleware("llm-observatory-api"))
	engine.Use(ginRequestLogger())

	engine.GET("/", helloworld)
	engine.GET("/healthz", healthz)
	engine.GET("/readyz", server.readyz)

	protected := engine.Group("/")
	protected.Use(requestBodyLimit(server.config.RequestBodyLimitBytes))
	protected.Use(server.apiKeyAuth())
	protected.POST("/chat", server.handlePrompt)

	engine.GET("/metrics", gin.WrapH(promhttp.Handler()))

	return engine
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

	config := loadConfig()
	client := newHTTPClient(getEnvInt("OLLAMA_TIMEOUT_SECONDS", defaultOllamaTimeoutSeconds))
	server := newServer(config, client)

	shutdownTracer, err := initTracer(context.Background(), config)
	if err != nil {
		slog.Error("failed to initialize tracing", "error", err.Error())
	} else {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdownTracer(ctx); err != nil {
				slog.Error("failed to shutdown tracing", "error", err.Error())
			}
		}()
	}

	addr := "0.0.0.0:8080"
	slog.Info("server starting", "addr", addr, "default_model", config.DefaultModel, "ollama_url", config.OllamaChatURL)
	if err := newRouter(server).Run(addr); err != nil {
		slog.Error("server stopped", "error", err.Error())
	}
}
