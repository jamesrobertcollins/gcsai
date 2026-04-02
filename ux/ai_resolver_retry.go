package ux

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"github.com/richardwilkes/toolbox/v2/i18n"
	"github.com/richardwilkes/unison"
)

type aiPlanResolutionResult struct {
	Parsed       bool
	Plan         aiActionPlan
	ResolvedPlan aiActionPlan
	RetryItems   []aiRetryItem
	Warnings     []string
}

type aiLocalChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func aiNormalizeLocalRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "model":
		return "assistant"
	case "assistant", "user", "system", "tool":
		return strings.ToLower(strings.TrimSpace(role))
	default:
		return "assistant"
	}
}

func aiMarshalLocalContent(content *genai.Content) string {
	var builder strings.Builder
	for _, part := range content.Parts {
		if txt, ok := part.(genai.Text); ok {
			builder.WriteString(string(txt))
		}
	}
	return strings.TrimSpace(builder.String())
}

func (d *aiChatDockable) buildLocalChatMessagesFromHistory(systemPrompt string, history []*genai.Content, userPrompt string) []aiLocalChatMessage {
	messages := []aiLocalChatMessage{{Role: "system", Content: systemPrompt}}
	for _, entry := range history {
		contentText := aiMarshalLocalContent(entry)
		if contentText == "" {
			continue
		}
		messages = append(messages, aiLocalChatMessage{Role: aiNormalizeLocalRole(entry.Role), Content: contentText})
	}
	return append(messages, aiLocalChatMessage{Role: "user", Content: userPrompt})
}

func (d *aiChatDockable) queryLocalModel(endpoint, model string, messages []aiLocalChatMessage, schema any) (string, error) {
	reqBody := struct {
		Model    string               `json:"model"`
		Messages []aiLocalChatMessage `json:"messages"`
		Format   any                  `json:"format,omitempty"`
		Options  any                  `json:"options,omitempty"`
		Stream   bool                 `json:"stream"`
	}{
		Model:    model,
		Messages: messages,
		Format:   schema,
		Options:  map[string]any{"temperature": 0},
		Stream:   false,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf(i18n.Text("Error preparing local request: %v"), err)
	}

	paths := []string{"/api/chat", "/api/generate"}
	for _, path := range paths {
		debugPath := endpoint + path
		unison.InvokeTask(func() {
			d.addMessage("Debug", fmt.Sprintf("POST %s (model=%s)", debugPath, model))
		})
		resp, postErr := http.Post(endpoint+path, "application/json", bytes.NewReader(body)) //nolint:noctx
		if postErr != nil {
			return "", fmt.Errorf(i18n.Text("Error querying local AI server: %v"), postErr)
		}
		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			responseBody, _ := io.ReadAll(resp.Body)
			return "", fmt.Errorf(i18n.Text("Local AI server returned %d: %s"), resp.StatusCode, strings.TrimSpace(string(responseBody)))
		}

		var result struct {
			Error   string `json:"error,omitempty"`
			Message *struct {
				Role    string `json:"role,omitempty"`
				Content string `json:"content,omitempty"`
			} `json:"message,omitempty"`
			Response string `json:"response,omitempty"`
			Choices  []struct {
				Text    string `json:"text,omitempty"`
				Message struct {
					Content string `json:"content,omitempty"`
				} `json:"message,omitempty"`
			} `json:"choices,omitempty"`
		}
		if decodeErr := json.NewDecoder(resp.Body).Decode(&result); decodeErr != nil {
			return "", fmt.Errorf(i18n.Text("Error parsing local AI response: %v"), decodeErr)
		}
		if result.Error != "" {
			return "", fmt.Errorf(i18n.Text("Local AI server error: %s"), result.Error)
		}

		responseText := strings.TrimSpace(result.Response)
		if responseText == "" && result.Message != nil {
			responseText = strings.TrimSpace(result.Message.Content)
		}
		if responseText == "" && len(result.Choices) > 0 {
			responseText = strings.TrimSpace(result.Choices[0].Text)
			if responseText == "" {
				responseText = strings.TrimSpace(result.Choices[0].Message.Content)
			}
		}
		if responseText == "" {
			return "", errors.New(i18n.Text("Local AI server returned no text."))
		}
		return responseText, nil
	}
	return "", errors.New(i18n.Text("Local AI server did not respond."))
}

func (d *aiChatDockable) resolveAIActionPlanResult(plan aiActionPlan) (aiPlanResolutionResult, error) {
	catalog, err := d.aiLibraryCatalog()
	if err != nil {
		return aiPlanResolutionResult{}, err
	}
	resolvedPlan, retryItems, warnings := catalog.resolveAIActionPlan(plan)
	return aiPlanResolutionResult{
		Parsed:       true,
		Plan:         plan,
		ResolvedPlan: resolvedPlan,
		RetryItems:   retryItems,
		Warnings:     warnings,
	}, nil
}

func (d *aiChatDockable) resolveAIResponseText(responseText string) (aiPlanResolutionResult, error) {
	plan, ok := d.parseAIActionPlan(responseText)
	if !ok {
		return aiPlanResolutionResult{Parsed: false}, nil
	}
	return d.resolveAIActionPlanResult(plan)
}

func aiBuildLocalResolverAlternativePrompt(items []aiRetryItem) string {
	var builder strings.Builder
	builder.WriteString("The following traits do not exist in the GURPS 4e library:\n")
	for _, item := range items {
		builder.WriteString("- ")
		builder.WriteString(aiCategorySingular(item.Category))
		builder.WriteString(": ")
		builder.WriteString(strconvQuote(item.Name))
		if strings.TrimSpace(item.Notes) != "" {
			builder.WriteString(" | notes=")
			builder.WriteString(strconvQuote(item.Notes))
		}
		if strings.TrimSpace(item.Points) != "" {
			builder.WriteString(" | points=")
			builder.WriteString(strconvQuote(item.Points))
		}
		if item.Quantity != 0 {
			builder.WriteString(fmt.Sprintf(" | quantity=%d", item.Quantity))
		}
		if len(item.Candidates) > 0 {
			candidateNames := make([]string, 0, len(item.Candidates))
			for _, candidate := range item.Candidates {
				candidateNames = append(candidateNames, candidate.Name)
			}
			builder.WriteString(" | close matches: ")
			builder.WriteString(strings.Join(candidateNames, ", "))
		}
		builder.WriteByte('\n')
	}
	builder.WriteString("Please provide valid GURPS 4e alternatives for these.\n")
	builder.WriteString("Return ONLY a single JSON object with replacement entries using the same category fields (advantages, disadvantages, quirks, skills, equipment).\n")
	builder.WriteString("Use valid GURPS 4e names, leave \"id\" empty, and preserve points, quantity, and notes when they still fit the replacement.\n")
	return builder.String()
}

func aiActionPlanWithoutRetryItems(plan aiActionPlan, retryItems []aiRetryItem) aiActionPlan {
	keys := make(map[string]struct{}, len(retryItems))
	for _, item := range retryItems {
		keys[aiActionPlanItemKey(item.Category, item.Name, item.Notes, item.Points, item.Quantity)] = struct{}{}
	}
	out := aiActionPlan{
		Profile:    plan.Profile,
		Attributes: append([]aiAttributeAction(nil), plan.Attributes...),
		SpendAllCP: plan.SpendAllCP,
	}
	for _, action := range plan.Advantages {
		if _, exists := keys[aiActionPlanItemKey(string(aiLibraryCategoryAdvantage), action.Name.String(), action.Notes.String(), action.Points.String(), action.Quantity.Int())]; !exists {
			out.Advantages = append(out.Advantages, action)
		}
	}
	for _, action := range plan.Disadvantages {
		if _, exists := keys[aiActionPlanItemKey(string(aiLibraryCategoryDisadvantage), action.Name.String(), action.Notes.String(), action.Points.String(), action.Quantity.Int())]; !exists {
			out.Disadvantages = append(out.Disadvantages, action)
		}
	}
	for _, action := range plan.Quirks {
		if _, exists := keys[aiActionPlanItemKey(string(aiLibraryCategoryQuirk), action.Name.String(), action.Notes.String(), action.Points.String(), action.Quantity.Int())]; !exists {
			out.Quirks = append(out.Quirks, action)
		}
	}
	for _, action := range plan.Skills {
		points := firstNonEmptyString(action.Points.String(), action.Value.String())
		if _, exists := keys[aiActionPlanItemKey(string(aiLibraryCategorySkill), action.Name.String(), action.Notes.String(), points, 0)]; !exists {
			out.Skills = append(out.Skills, action)
		}
	}
	for _, action := range plan.Equipment {
		if _, exists := keys[aiActionPlanItemKey(string(aiLibraryCategoryEquipment), action.Name.String(), action.Notes.String(), action.Points.String(), action.Quantity.Int())]; !exists {
			out.Equipment = append(out.Equipment, action)
		}
	}
	return out
}

func aiActionPlanItemKey(category, name, notes, points string, quantity int) string {
	return strings.Join([]string{
		aiCategoryJSONField(category),
		normalizeLookupText(strings.TrimSpace(name)),
		normalizeLookupText(strings.TrimSpace(notes)),
		strings.TrimSpace(points),
		fmt.Sprintf("%d", quantity),
	}, "|")
}

func aiActionPlanItemCount(plan aiActionPlan) int {
	return len(plan.Advantages) + len(plan.Disadvantages) + len(plan.Quirks) + len(plan.Skills) + len(plan.Equipment)
}

func strconvQuote(text string) string {
	encoded, err := json.Marshal(strings.TrimSpace(text))
	if err != nil {
		return fmt.Sprintf("%q", strings.TrimSpace(text))
	}
	return string(encoded)
}
