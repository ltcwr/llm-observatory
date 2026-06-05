package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/gin-gonic/gin"
)

type Chat struct {
	Prompt         string  `json:"prompt"`
	Answer         string  `json:"answer"`
	TotalDuration  int64   `json:"total_duration_ns"`
	EvalCount      int     `json:"eval_count"`
	EvalDuration   int64   `json:"eval_duration_ns"`
	TokensPerSec   float64 `json:"tokens_per_sec"`
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
	LoadDuration       int       `json:"load_duration"`
	PromptEvalCount    int       `json:"prompt_eval_count"`
	PromptEvalDuration int       `json:"prompt_eval_duration"`
	EvalCount          int       `json:"eval_count"`
	EvalDuration       int64     `json:"eval_duration"`
}

type OllamaRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

const ollamaURL = "http://localhost:11434/api/chat"

func helloworld(c *gin.Context) { // for debuging
	c.String(
		http.StatusOK,
		"Hello World! Time : %s",
		time.Now().Format(time.RFC3339Nano),
	)
}

func handlePrompt(c *gin.Context) {
	var p Chat
	requestID := c.GetString("request_id")

	if err := c.ShouldBindJSON(&p); err != nil {
		slog.Error("invalid request body", "request_id", requestID, "error", err.Error())
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	slog.Info(
    "chat request",
    "request_id", requestID,
    "prompt", p.Prompt,
)

	reqBody := OllamaRequest{
		Model: "qwen2.5:0.5b",
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
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}

	slog.Info(
    "ollama request",
    "request_id", requestID,
    "model", reqBody.Model,
)


	resp, err := http.Post(
		ollamaURL,
		"application/json",
		bytes.NewBuffer(body),
	)
	if err != nil {
		slog.Error("ollama request failed", "request_id", requestID, "error", err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}

	defer resp.Body.Close()

	var ollamaResp Response

	err = json.NewDecoder(resp.Body).Decode(&ollamaResp)
	if err != nil {
		slog.Error("failed to decode ollama response", "request_id", requestID, "error", err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}

	p.Answer = ollamaResp.Message.Content
	p.TotalDuration = ollamaResp.TotalDuration
	p.EvalCount = ollamaResp.EvalCount
	p.EvalDuration = ollamaResp.EvalDuration
	if ollamaResp.EvalDuration > 0 {
		p.TokensPerSec = float64(ollamaResp.EvalCount) * 1e9 / float64(ollamaResp.EvalDuration)
	}

	slog.Info("chat response",
		"request_id", requestID,
		"eval_count", p.EvalCount,
		"total_duration_ns", p.TotalDuration,
		"eval_duration_ns", p.EvalDuration,
		"tokens_per_sec", p.TokensPerSec,
	)

	c.JSON(http.StatusOK, p)
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
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	engine := gin.New()

	engine.Use(ginRequestLogger())

	engine.GET("/", helloworld)

	engine.POST("/chat", handlePrompt)

	slog.Info("server starting", "addr", "localhost:8080")
	engine.Run("localhost:8080")
}
