package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

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

	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	reqBody := OllamaRequest{
		Model: "qwen3:1.7b",
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

	resp, err := http.Post(
		ollamaURL,
		"application/json",
		bytes.NewBuffer(body),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}

	defer resp.Body.Close()

	var ollamaResp Response

	err = json.NewDecoder(resp.Body).Decode(&ollamaResp)
	if err != nil {
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
	c.JSON(http.StatusOK, p)
}

func main() {
	engine := gin.New()

	engine.GET("/", helloworld)


	engine.POST("/chat", handlePrompt)

	engine.Run("localhost:8080")
}
