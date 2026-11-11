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

func TestOpenAIRequestAndStructuredResult(t *testing.T) {
	for _, kind := range []string{"image", "text"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer secret" {
					t.Error("invalid request")
				}
				var payload struct {
					Model string `json:"model"`
					Store bool   `json:"store"`
					Input []struct {
						Content []map[string]any `json:"content"`
					} `json:"input"`
					Text struct {
						Format struct {
							Type   string `json:"type"`
							Strict bool   `json:"strict"`
						} `json:"format"`
					} `json:"text"`
				}
				if json.NewDecoder(r.Body).Decode(&payload) != nil {
					t.Fatal("invalid JSON")
				}
				if payload.Model != "test-model" || payload.Store || payload.Text.Format.Type != "json_schema" || !payload.Text.Format.Strict {
					t.Error("missing structured output or privacy setting")
				}
				want := 1
				if kind == "image" {
					want = 2
				}
				if len(payload.Input) != 1 || len(payload.Input[0].Content) != want {
					t.Error("missing input content")
				}
				if kind == "image" && !strings.HasPrefix(fmt.Sprint(payload.Input[0].Content[1]["image_url"]), "data:image/png;base64,") {
					t.Error("missing image")
				}
				w.Header().Set("x-request-id", "req_test")
				json.NewEncoder(w).Encode(map[string]any{"status": "completed", "model": "response-model", "usage": map[string]int{"input_tokens": 12, "output_tokens": 20}, "output": []any{map[string]any{"content": []any{map[string]string{"type": "output_text", "text": `{"title":"제목","description":"설명","extracted_text":"","tags":[" Cat ","cat","웃음"],"safety_rating":"sfw"}`}}}}})
			}))
			defer server.Close()
			client := NewClient("secret", time.Second)
			client.endpoint = server.URL + "/v1/responses"
			response, err := client.Analyze(context.Background(), Input{Model: "test-model", ContentType: kind, Image: []byte("image"), MIME: "image/png"})
			if err != nil {
				t.Fatal(err)
			}
			if response.Model != "response-model" || response.RequestID != "req_test" || response.InputTokens != 12 || response.Result.SafetyRating != "sfw" || len(response.Result.Tags) != 2 || response.Result.Tags[0] != "Cat" {
				t.Fatalf("unexpected response: %+v", response)
			}
		})
	}
}

func TestOpenAIFailures(t *testing.T) {
	for _, entry := range []struct {
		name       string
		status     int
		body, code string
		retry      bool
	}{
		{"auth", 401, `secret provider error`, "openai_auth_failed", false},
		{"rate", 429, `private error`, "openai_rate_limited", true},
		{"server", 503, `private error`, "openai_unavailable", true},
		{"incomplete", 200, `{"status":"incomplete","model":"test"}`, "invalid_openai_response", false},
		{"refusal", 200, `{"status":"completed","model":"test","output":[{"content":[{"type":"refusal"}]}]}`, "openai_refused", false},
		{"malformed", 200, `{"status":"completed","model":"test","output":[{"content":[{"type":"output_text","text":"{}"}]}]}`, "invalid_analysis_result", false},
		{"null-field", 200, `{"status":"completed","model":"test","output":[{"content":[{"type":"output_text","text":"{\"title\":null,\"description\":\"\",\"extracted_text\":\"\",\"tags\":[]}"}]}]}`, "invalid_analysis_result", false},
	} {
		t.Run(entry.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(entry.status); fmt.Fprint(w, entry.body) }))
			defer server.Close()
			client := NewClient("secret", time.Second)
			client.endpoint = server.URL
			_, err := client.Analyze(context.Background(), Input{ContentType: "text"})
			if err == nil {
				t.Fatal("expected failure")
			}
			code, retry := failureDetails(err)
			if code != entry.code || retry != entry.retry {
				t.Fatalf("wrong safe failure: %s %v", code, retry)
			}
		})
	}
	for _, result := range []Result{{Title: strings.Repeat("한", 201), Tags: []string{}}, {Tags: []string{""}}, {Tags: []string{strings.Repeat("x", 51)}}, {ExtractedText: "invented OCR", Tags: []string{}}, {Tags: nil}} {
		result.SafetyRating = "sfw"
		if result.validate("text") == nil {
			t.Fatal("invalid output accepted")
		}
	}
}

func TestOpenAISafetyRating(t *testing.T) {
	for _, rating := range []string{`"sfw"`, `"nsfw"`, `"unknown"`, `"safe"`, `""`, `null`, ``} {
		t.Run(rating, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				result := `{"title":"Title","description":"","extracted_text":"","tags":[]`
				if rating != "" {
					result += `,"safety_rating":` + rating
				}
				result += `}`
				json.NewEncoder(w).Encode(map[string]any{"status": "completed", "model": "test-model", "output": []any{map[string]any{"content": []any{map[string]string{"type": "output_text", "text": result}}}}})
			}))
			defer server.Close()
			client := NewClient("secret", time.Second)
			client.endpoint = server.URL
			res, err := client.Analyze(context.Background(), Input{ContentType: "text", Model: "test-model", Body: "evidence"})
			valid := rating == `"sfw"` || rating == `"nsfw"` || rating == `"unknown"`
			if valid && (err != nil || `"`+res.Result.SafetyRating+`"` != rating) {
				t.Fatalf("valid rating lost: %+v %v", res, err)
			}
			if !valid {
				code, retry := failureDetails(err)
				if err == nil || code != "invalid_analysis_result" || retry {
					t.Fatalf("invalid rating accepted: %+v %v", res, err)
				}
			}
		})
	}
}
