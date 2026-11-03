package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const Model = "text-embedding-3-large"
const Dimensions = 3072
const InputVersion = "v1"

type Result struct {
	Vector []float32
	Tokens int64
}
type Embedder interface {
	Embed(context.Context, string) (Result, error)
}
type Failure struct{ Retryable bool }

func (e *Failure) Error() string { return "embedding request failed" }

type Client struct {
	key, endpoint string
	http          *http.Client
}

func NewClient(key string) *Client {
	return &Client{key: key, endpoint: "https://api.openai.com/v1/embeddings", http: &http.Client{Timeout: 15 * time.Second}}
}
func (c *Client) Embed(ctx context.Context, input string) (Result, error) {
	payload, err := json.Marshal(map[string]any{"model": Model, "input": input, "dimensions": Dimensions, "encoding_format": "float"})
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return Result{}, &Failure{}
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return Result{}, &Failure{Retryable: true}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Result{}, &Failure{Retryable: res.StatusCode == 429 || res.StatusCode >= 500}
	}
	var data struct {
		Model string `json:"model"`
		Data  []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			TotalTokens int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&data) != nil || data.Model != Model || len(data.Data) != 1 || data.Data[0].Index != 0 || !Valid(data.Data[0].Embedding) || data.Usage.TotalTokens < 0 {
		return Result{}, &Failure{}
	}
	return Result{Vector: data.Data[0].Embedding, Tokens: data.Usage.TotalTokens}, nil
}
func Valid(v []float32) bool {
	if len(v) != Dimensions {
		return false
	}
	var norm float64
	for _, x := range v {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return false
		}
		norm += float64(x) * float64(x)
	}
	return norm > 0
}
func Literal(v []float32) string {
	var s strings.Builder
	s.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			s.WriteByte(',')
		}
		s.WriteString(strconv.FormatFloat(float64(x), 'g', -1, 32))
	}
	s.WriteByte(']')
	return s.String()
}
func retryable(err error) bool { var f *Failure; return errors.As(err, &f) && f.Retryable }
