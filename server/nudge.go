package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/msfoundry/commit/store"
)

func generateNudgeMessage(ctx context.Context, apiKey, model string, c *store.Commitment) (string, error) {
	prompt := fmt.Sprintf(`Write a short, natural WhatsApp follow-up message (1-2 sentences max) for this situation:

- %s promised to: %s
- Context: %s
- Original quote: "%s"
- This was %s ago

The message should be polite, casual, and natural — like something a real person would type on WhatsApp. Don't be formal or robotic. Don't use greetings like "Hi" or "Hey there". Just a friendly nudge about the thing.

Return ONLY the message text, nothing else.`, c.PersonName, c.Title, c.Context, c.SourceQuote, c.SourceTime)

	text, err := callClaudeSimple(ctx, apiKey, model, prompt, 256)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

func callClaudeSimple(ctx context.Context, apiKey, model, prompt string, maxTokens int) (string, error) {
	reqBody := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode == 404 && strings.Contains(string(respBody), "not_found_error") {
		return "", fmt.Errorf("model_not_found:%s", model)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("api error %d: %s", resp.StatusCode, string(respBody))
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

// generateDraftReply writes a reply in the USER's own voice, learned few-shot
// from their past messages. It only drafts text — it never sends anything.
func generateDraftReply(ctx context.Context, apiKey, model, personName string, thread []*store.Message, samples []string) (string, error) {
	var sb strings.Builder
	sb.WriteString(`You are drafting a WhatsApp reply on behalf of the user (the person marked [Me]). Match THEIR voice exactly — tone, length, capitalization, punctuation, emoji habits, and language (including Hinglish / mixed Hindi-English if that's how they write). Write only what the user would plausibly type next. Keep it short like a real WhatsApp message. Do not add greetings or sign-offs unless the user clearly uses them.

`)
	if len(samples) > 0 {
		sb.WriteString("HOW THE USER WRITES — real messages they have sent (study the style, not the content):\n")
		for _, s := range samples {
			sb.WriteString("- " + strings.ReplaceAll(s, "\n", " ") + "\n")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("THE CONVERSATION so far (most recent last)")
	if personName != "" {
		sb.WriteString(", replying to " + personName)
	}
	sb.WriteString(":\n")
	for _, m := range thread {
		who := m.SenderName
		if m.IsFromMe {
			who = "[Me]"
		}
		if who == "" {
			who = "Them"
		}
		sb.WriteString(who + ": " + strings.ReplaceAll(m.Content, "\n", " ") + "\n")
	}
	sb.WriteString("\nDraft the user's next reply. Return ONLY the message text — no quotes, no explanation, no alternatives.")

	text, err := callClaudeSimple(ctx, apiKey, model, sb.String(), 400)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

// callDraftWithFallback drafts a reply, falling back if the model isn't found.
func (s *Server) callDraftWithFallback(ctx context.Context, apiKey, personName string, thread []*store.Message, samples []string) (string, error) {
	model := s.db.GetModel()
	text, err := generateDraftReply(ctx, apiKey, model, personName, thread, samples)
	if err != nil && strings.Contains(err.Error(), "model_not_found") && model != store.FallbackModel {
		log.Printf("model %s not available for draft, falling back to %s", model, store.FallbackModel)
		s.db.SetModel(store.FallbackModel)
		return generateDraftReply(ctx, apiKey, store.FallbackModel, personName, thread, samples)
	}
	return text, err
}

// callNudgeWithFallback tries the preferred model, falls back if not found
func (s *Server) callNudgeWithFallback(ctx context.Context, apiKey string, c *store.Commitment) (string, error) {
	model := s.db.GetModel()
	text, err := generateNudgeMessage(ctx, apiKey, model, c)
	if err != nil && strings.Contains(err.Error(), "model_not_found") && model != store.FallbackModel {
		log.Printf("model %s not available for nudge, falling back to %s", model, store.FallbackModel)
		s.db.SetModel(store.FallbackModel)
		return generateNudgeMessage(ctx, apiKey, store.FallbackModel, c)
	}
	return text, err
}
