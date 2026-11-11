package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDecisionsPipeline(t *testing.T) {
	for _, kind := range []string{"text", "image"} {
		for _, rating := range []string{"sfw", "nsfw", "unknown"} {
			t.Run(kind+"/"+rating, func(t *testing.T) {
				var calls []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls = append(calls, r.URL.Path)
					if r.Header.Get("Authorization") != "Bearer secret" {
						t.Error("missing authorization")
					}
					var payload map[string]any
					if json.NewDecoder(r.Body).Decode(&payload) != nil {
						t.Error("invalid JSON")
					}
					switch r.URL.Path {
					case "/v1/decisions":
						if payload["model"] != DecisionsModel {
							t.Error("incorrect classifier model")
						}
						content := payload["input"].([]any)[0].(map[string]any)["content"].([]any)
						if !strings.Contains(content[0].(map[string]any)["text"].(string), "untrusted evidence") {
							t.Error("missing original metadata")
						}
						want := 1
						if kind == "image" {
							want = 2
						}
						if len(content) != want {
							t.Error("missing evidence")
						}
						if kind == "image" {
							image := content[1].(map[string]any)
							if !strings.HasPrefix(image["image_url"].(string), "data:image/png;base64,") || image["detail"] != nil {
								t.Error("invalid Decisions image")
							}
						}
						question := payload["questions"].([]any)[0].(map[string]any)
						if question["name"] != "safety_rating" || question["type"] != "choice" || len(question["choices"].([]any)) != 3 {
							t.Error("invalid safety question")
						}
						w.Header().Set("x-request-id", "req_decision")
						fmt.Fprintf(w, `{"model":"gpt-6-luna","answers":[{"type":"choice","name":"safety_rating","choice":%q,"confidence":0.9}],"usage":{"input_tokens":15,"output_tokens":0}}`, rating)
					case "/v1/responses":
						if payload["model"] != "metadata-model" || strings.Contains(string(mustJSON(t, payload["text"])), "safety_rating") {
							t.Error("metadata model must not classify safety")
						}
						json.NewEncoder(w).Encode(map[string]any{"status": "completed", "model": "metadata-model", "usage": map[string]int{"input_tokens": 10, "output_tokens": 20}, "output": []any{map[string]any{"content": []any{map[string]string{"type": "output_text", "text": `{"title":"Title","description":"","extracted_text":"","tags":[]}`}}}}})
					default:
						t.Error("unexpected endpoint")
					}
				}))
				defer server.Close()
				pipeline := NewPipeline("secret", time.Second)
				pipeline.client.endpoint = server.URL + "/v1/responses"
				pipeline.decisionsEndpoint = server.URL + "/v1/decisions"
				response, err := pipeline.Analyze(context.Background(), Input{Model: "metadata-model", ContentType: kind, Body: "untrusted evidence", Image: []byte("png"), MIME: "image/png"})
				if err != nil {
					t.Fatal(err)
				}
				decision := response.Result.SafetyDecision
				if response.Result.SafetyRating != rating || decision == nil || decision.Model != DecisionsModel || decision.RequestID != "req_decision" || decision.Confidence != 0.9 || decision.InputTokens != 15 || response.InputTokens != 10 || response.OutputTokens != 20 {
					t.Fatalf("missing classification or mixed usage: %+v", response)
				}
				if strings.Join(calls, ",") != "/v1/decisions,/v1/responses" {
					t.Fatal("pipeline not wired", calls)
				}
			})
		}
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDecisionsFailures(t *testing.T) {
	valid := `{"model":"gpt-6-luna","answers":[{"type":"choice","name":"safety_rating","choice":"sfw","confidence":0.9}],"usage":{"input_tokens":15,"output_tokens":0}}`
	for _, test := range []struct {
		name       string
		status     int
		body, code string
		retry      bool
	}{
		{"refusal", 200, `{"model":"gpt-6-luna","answers":[{"type":"refusal","name":"safety_rating"}]}`, "openai_decisions_refused", false},
		{"missing answer", 200, `{"model":"gpt-6-luna","answers":[]}`, "invalid_decisions_response", false},
		{"invalid choice", 200, strings.Replace(valid, `"sfw"`, `"safe"`, 1), "invalid_decisions_response", false},
		{"missing confidence", 200, strings.Replace(valid, `,"confidence":0.9`, "", 1), "invalid_decisions_response", false},
		{"invalid confidence", 200, strings.Replace(valid, `0.9`, `1.1`, 1), "invalid_decisions_response", false},
		{"wrong question", 200, strings.Replace(valid, `safety_rating`, `other`, 1), "invalid_decisions_response", false},
		{"missing usage", 200, strings.Replace(valid, `"input_tokens":15`, `"input_tokens":null`, 1), "invalid_decisions_response", false},
		{"malformed", 200, `{`, "invalid_decisions_response", false},
		{"rate limit", 429, "private provider error", "openai_rate_limited", true},
		{"server", 503, "private provider error", "openai_unavailable", true},
		{"auth", 401, "private provider error", "openai_auth_failed", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			p := NewPipeline("secret", time.Second)
			p.decisionsEndpoint = server.URL
			p.client.endpoint = server.URL
			response, err := p.Analyze(context.Background(), Input{ContentType: "text"})
			code, retry := failureDetails(err)
			if err == nil || code != test.code || retry != test.retry || response.Result.SafetyRating != "" || calls != 1 {
				t.Fatalf("unsafe fallback or unexpected retry: %s %v %+v calls=%d", code, err, response, calls)
			}
		})
	}
	for _, input := range []Input{{ContentType: "audio"}, {ContentType: "image"}, {ContentType: "image", Image: []byte("gif"), MIME: "image/gif"}} {
		p := NewPipeline("secret", time.Second)
		if _, err := p.Analyze(context.Background(), input); err == nil {
			t.Fatal("invalid or unsupported media accepted")
		}
	}
}
