package server

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/msfoundry/commit/llm"
	"github.com/msfoundry/commit/store"
)

func generateNudgeMessage(ctx context.Context, provider, apiKey, model string, c *store.Commitment) (string, error) {
	prompt := fmt.Sprintf(`Write a short, natural WhatsApp follow-up message (1-2 sentences max) for this situation:

- %s promised to: %s
- Context: %s
- Original quote: "%s"
- This was %s ago

The message should be polite, casual, and natural — like something a real person would type on WhatsApp. Don't be formal or robotic. Don't use greetings like "Hi" or "Hey there". Just a friendly nudge about the thing.

Return ONLY the message text, nothing else.`, c.PersonName, c.Title, c.Context, c.SourceQuote, c.SourceTime)

	text, err := llm.Complete(ctx, provider, apiKey, model, prompt, 256)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

// callNudgeWithFallback drafts a nudge; Anthropic-only model fallback.
func (s *Server) callNudgeWithFallback(ctx context.Context, apiKey string, c *store.Commitment) (string, error) {
	provider := s.db.GetProvider()
	model := s.db.GetModel()
	text, err := generateNudgeMessage(ctx, provider, apiKey, model, c)
	if err != nil && provider == store.ProviderAnthropic && strings.Contains(err.Error(), "model_not_found") && model != store.FallbackModel {
		log.Printf("model %s not available for nudge, falling back to %s", model, store.FallbackModel)
		s.db.SetModel(store.FallbackModel)
		return generateNudgeMessage(ctx, provider, apiKey, store.FallbackModel, c)
	}
	return text, err
}
