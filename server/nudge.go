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

// generateDraftReply writes a reply in the USER's own voice, learned few-shot
// from their past messages. It only drafts text — it never sends anything.
func generateDraftReply(ctx context.Context, provider, apiKey, model, personName string, thread []*store.Message, samples []string) (string, error) {
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

	text, err := llm.Complete(ctx, provider, apiKey, model, sb.String(), 400)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

// callDraftWithFallback drafts a reply; Anthropic-only model fallback.
func (s *Server) callDraftWithFallback(ctx context.Context, apiKey, personName string, thread []*store.Message, samples []string) (string, error) {
	provider := s.db.GetProvider()
	model := s.db.GetModel()
	text, err := generateDraftReply(ctx, provider, apiKey, model, personName, thread, samples)
	if err != nil && provider == store.ProviderAnthropic && strings.Contains(err.Error(), "model_not_found") && model != store.FallbackModel {
		log.Printf("model %s not available for draft, falling back to %s", model, store.FallbackModel)
		s.db.SetModel(store.FallbackModel)
		return generateDraftReply(ctx, provider, apiKey, store.FallbackModel, personName, thread, samples)
	}
	return text, err
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
