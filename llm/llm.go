// Package llm is a tiny provider-agnostic completion client. It supports
// Anthropic (Claude) and OpenAI behind one Complete() call so the rest of the
// app doesn't care which one is configured.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	Anthropic = "anthropic"
	OpenAI    = "openai"

	anthropicURL = "https://api.anthropic.com/v1/messages"
	openaiURL    = "https://api.openai.com/v1/chat/completions"
)

// Complete sends a single user-message prompt to the chosen provider and
// returns the assistant text. Errors whose message contains "model_not_found"
// signal the caller it may retry with a fallback model.
func Complete(ctx context.Context, provider, apiKey, model, prompt string, maxTokens int) (string, error) {
	if provider == OpenAI {
		return completeOpenAI(ctx, apiKey, model, prompt, maxTokens)
	}
	return completeAnthropic(ctx, apiKey, model, prompt, maxTokens)
}

func completeAnthropic(ctx context.Context, apiKey, model, prompt string, maxTokens int) (string, error) {
	reqBody := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
	}
	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", anthropicURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 429 {
		return "", fmt.Errorf("rate limited (429)")
	}
	if resp.StatusCode == 404 && strings.Contains(string(respBody), "not_found_error") {
		return "", fmt.Errorf("model_not_found: %s", model)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("anthropic api error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("empty response")
	}
	return result.Content[0].Text, nil
}

func completeOpenAI(ctx context.Context, apiKey, model, prompt string, maxTokens int) (string, error) {
	reqBody := map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	}
	if maxTokens > 0 {
		// Modern field — accepted by gpt-4o-mini and the reasoning models alike.
		reqBody["max_completion_tokens"] = maxTokens
	}
	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", openaiURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 429 {
		return "", fmt.Errorf("rate limited (429)")
	}
	if (resp.StatusCode == 404 || resp.StatusCode == 400) &&
		(strings.Contains(string(respBody), "model_not_found") || strings.Contains(string(respBody), "does not exist")) {
		return "", fmt.Errorf("model_not_found: %s", model)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("openai api error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty response")
	}
	return result.Choices[0].Message.Content, nil
}
