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
	return strings.TrimSpace(aiNormalizeExternalText("ai.history.content", builder.String()))
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

func buildLocalStatelessMessages(systemPrompt, userPrompt string) []aiLocalChatMessage {
	return []aiLocalChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}
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
		return aiNormalizeExternalText("local.response", responseText), nil
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
	builder.WriteString("The following GURPS 4e items could not be resolved to exact library entries:\n")
	for _, item := range items {
		builder.WriteString("- ")
		builder.WriteString(aiCategorySingular(item.Category))
		builder.WriteString(": ")
		builder.WriteString(strconvQuote(item.Name))
		if strings.TrimSpace(item.Notes) != "" {
			builder.WriteString(" | notes=")
			builder.WriteString(strconvQuote(item.Notes))
		}
		if strings.TrimSpace(item.Description) != "" {
			builder.WriteString(" | description=")
			builder.WriteString(strconvQuote(item.Description))
		}
		if strings.TrimSpace(item.Points) != "" {
			builder.WriteString(" | points=")
			builder.WriteString(strconvQuote(item.Points))
		}
		if item.Quantity != 0 {
			builder.WriteString(fmt.Sprintf(" | quantity=%d", item.Quantity))
		}
		if len(item.Candidates) > 0 {
			for _, candidate := range item.Candidates {
				builder.WriteString("\n  - candidate id=")
				builder.WriteString(candidate.ID)
				builder.WriteString(" | name=")
				builder.WriteString(candidate.Name)
				if len(candidate.Requires) > 0 {
					builder.WriteString(" | requires: ")
					builder.WriteString(strings.Join(candidate.Requires, ", "))
				}
			}
		} else if len(item.Similar) > 0 {
			builder.WriteString(" | alternatives: ")
			builder.WriteString(strings.Join(item.Similar, ", "))
		}
		builder.WriteByte('\n')
	}
	builder.WriteString("Return ONLY a single JSON object with replacement entries using the same category fields (advantages, disadvantages, quirks, skills, spells, equipment).\n")
	builder.WriteString("When candidates are listed, use the exact candidate id and candidate name shown.\n")
	builder.WriteString("Use valid GURPS 4e names, preserve points, quantity, notes, and description when they still fit the replacement, and use \"notes\" only for nameable values.\n")
	builder.WriteString("If no valid replacement exists, omit that item.\n")
	return builder.String()
}

func aiActionPlanWithoutRetryItems(plan aiActionPlan, retryItems []aiRetryItem) aiActionPlan {
	keys := make(map[string]struct{}, len(retryItems))
	for _, item := range retryItems {
		keys[aiActionPlanItemKey(item.Category, item.Name, item.Notes, item.Description, item.Points, item.Quantity)] = struct{}{}
	}
	out := aiActionPlan{
		Profile:    plan.Profile,
		Attributes: append([]aiAttributeAction(nil), plan.Attributes...),
		SpendAllCP: plan.SpendAllCP,
	}
	for _, action := range plan.Advantages {
		if _, exists := keys[aiActionPlanItemKey(string(aiLibraryCategoryAdvantage), action.Name.String(), action.Notes.String(), action.Description.String(), action.Points.String(), action.Quantity.Int())]; !exists {
			out.Advantages = append(out.Advantages, action)
		}
	}
	for _, action := range plan.Disadvantages {
		if _, exists := keys[aiActionPlanItemKey(string(aiLibraryCategoryDisadvantage), action.Name.String(), action.Notes.String(), action.Description.String(), action.Points.String(), action.Quantity.Int())]; !exists {
			out.Disadvantages = append(out.Disadvantages, action)
		}
	}
	for _, action := range plan.Quirks {
		if _, exists := keys[aiActionPlanItemKey(string(aiLibraryCategoryQuirk), action.Name.String(), action.Notes.String(), action.Description.String(), action.Points.String(), action.Quantity.Int())]; !exists {
			out.Quirks = append(out.Quirks, action)
		}
	}
	for _, action := range plan.Skills {
		points := firstNonEmptyString(action.Points.String(), action.Value.String())
		if _, exists := keys[aiActionPlanItemKey(string(aiLibraryCategorySkill), action.Name.String(), action.Notes.String(), action.Description.String(), points, 0)]; !exists {
			out.Skills = append(out.Skills, action)
		}
	}
	for _, action := range plan.Spells {
		points := firstNonEmptyString(action.Points.String(), action.Value.String())
		if _, exists := keys[aiActionPlanItemKey(string(aiLibraryCategorySpell), action.Name.String(), action.Notes.String(), action.Description.String(), points, 0)]; !exists {
			out.Spells = append(out.Spells, action)
		}
	}
	for _, action := range plan.Equipment {
		if _, exists := keys[aiActionPlanItemKey(string(aiLibraryCategoryEquipment), action.Name.String(), action.Notes.String(), action.Description.String(), action.Points.String(), action.Quantity.Int())]; !exists {
			out.Equipment = append(out.Equipment, action)
		}
	}
	return out
}

func aiActionPlanItemKey(category, name, notes, description, points string, quantity int) string {
	return strings.Join([]string{
		aiCategoryJSONField(category),
		normalizeLookupText(strings.TrimSpace(name)),
		normalizeLookupText(strings.TrimSpace(notes)),
		normalizeLookupText(strings.TrimSpace(description)),
		strings.TrimSpace(points),
		fmt.Sprintf("%d", quantity),
	}, "|")
}

func aiFilterCorrectionPlan(plan aiActionPlan, retryItems []aiRetryItem) aiActionPlan {
	var filtered aiActionPlan
	advantageItems, advantageUsed := aiRetryItemsForCategory(retryItems, string(aiLibraryCategoryAdvantage))
	disadvantageItems, disadvantageUsed := aiRetryItemsForCategory(retryItems, string(aiLibraryCategoryDisadvantage))
	quirkItems, quirkUsed := aiRetryItemsForCategory(retryItems, string(aiLibraryCategoryQuirk))
	skillItems, skillUsed := aiRetryItemsForCategory(retryItems, string(aiLibraryCategorySkill))
	spellItems, spellUsed := aiRetryItemsForCategory(retryItems, string(aiLibraryCategorySpell))
	equipmentItems, equipmentUsed := aiRetryItemsForCategory(retryItems, string(aiLibraryCategoryEquipment))

	for _, action := range plan.Advantages {
		if aiConsumeMatchingNamedCorrection(advantageItems, advantageUsed, string(aiLibraryCategoryAdvantage), action) {
			filtered.Advantages = append(filtered.Advantages, action)
		}
	}
	for _, action := range plan.Disadvantages {
		if aiConsumeMatchingNamedCorrection(disadvantageItems, disadvantageUsed, string(aiLibraryCategoryDisadvantage), action) {
			filtered.Disadvantages = append(filtered.Disadvantages, action)
		}
	}
	for _, action := range plan.Quirks {
		if aiConsumeMatchingNamedCorrection(quirkItems, quirkUsed, string(aiLibraryCategoryQuirk), action) {
			filtered.Quirks = append(filtered.Quirks, action)
		}
	}
	for _, action := range plan.Skills {
		if aiConsumeMatchingSkillCorrection(skillItems, skillUsed, action) {
			filtered.Skills = append(filtered.Skills, action)
		}
	}
	for _, action := range plan.Spells {
		if aiConsumeMatchingSpellCorrection(spellItems, spellUsed, action) {
			filtered.Spells = append(filtered.Spells, action)
		}
	}
	for _, action := range plan.Equipment {
		if aiConsumeMatchingNamedCorrection(equipmentItems, equipmentUsed, string(aiLibraryCategoryEquipment), action) {
			filtered.Equipment = append(filtered.Equipment, action)
		}
	}
	return filtered
}

func aiRetryItemsForCategory(retryItems []aiRetryItem, category string) ([]aiRetryItem, []bool) {
	category = aiCategoryJSONField(category)
	items := make([]aiRetryItem, 0, len(retryItems))
	for _, item := range retryItems {
		if aiCategoryJSONField(item.Category) == category {
			items = append(items, item)
		}
	}
	return items, make([]bool, len(items))
}

func aiConsumeMatchingNamedCorrection(items []aiRetryItem, used []bool, category string, action aiNamedAction) bool {
	return aiConsumeMatchingCorrection(items, used, category, normalizeAISelectionID(action.ID.String()), normalizeLookupText(action.Name.String()))
}

func aiConsumeMatchingSkillCorrection(items []aiRetryItem, used []bool, action aiSkillAction) bool {
	return aiConsumeMatchingCorrection(items, used, string(aiLibraryCategorySkill), normalizeAISelectionID(action.ID.String()), normalizeLookupText(action.Name.String()))
}

func aiConsumeMatchingSpellCorrection(items []aiRetryItem, used []bool, action aiSkillAction) bool {
	return aiConsumeMatchingCorrection(items, used, string(aiLibraryCategorySpell), normalizeAISelectionID(action.ID.String()), normalizeLookupText(action.Name.String()))
}

func aiConsumeMatchingCorrection(items []aiRetryItem, used []bool, category, id, name string) bool {
	for i, item := range items {
		if used[i] {
			continue
		}
		if !aiCorrectionActionMatchesRetryItem(category, id, name, item) {
			continue
		}
		used[i] = true
		return true
	}
	return false
}

func aiCorrectionActionMatchesRetryItem(category, id, name string, item aiRetryItem) bool {
	if aiCategoryJSONField(category) != aiCategoryJSONField(item.Category) {
		return false
	}
	if len(item.Candidates) > 0 {
		for _, candidate := range item.Candidates {
			if id != "" && normalizeAISelectionID(candidate.ID) == id {
				return true
			}
			if id == "" && name != "" && normalizeLookupText(candidate.Name) == name {
				return true
			}
		}
		return false
	}
	if len(item.Similar) > 0 {
		for _, similar := range item.Similar {
			if name != "" && normalizeLookupText(similar) == name {
				return true
			}
		}
		return false
	}
	return name != ""
}

func aiActionPlanItemCount(plan aiActionPlan) int {
	return len(plan.Advantages) + len(plan.Disadvantages) + len(plan.Quirks) + len(plan.Skills) + len(plan.Spells) + len(plan.Equipment)
}

type aiResolvedCorrection struct {
	Category   string
	Requested  string
	Resolved   string
	ResolvedID string
}

func aiCollectResolvedCorrections(plan aiActionPlan, retryItems []aiRetryItem) []aiResolvedCorrection {
	corrections := make([]aiResolvedCorrection, 0, aiActionPlanItemCount(plan))
	advantageItems, advantageUsed := aiRetryItemsForCategory(retryItems, string(aiLibraryCategoryAdvantage))
	disadvantageItems, disadvantageUsed := aiRetryItemsForCategory(retryItems, string(aiLibraryCategoryDisadvantage))
	quirkItems, quirkUsed := aiRetryItemsForCategory(retryItems, string(aiLibraryCategoryQuirk))
	skillItems, skillUsed := aiRetryItemsForCategory(retryItems, string(aiLibraryCategorySkill))
	spellItems, spellUsed := aiRetryItemsForCategory(retryItems, string(aiLibraryCategorySpell))
	equipmentItems, equipmentUsed := aiRetryItemsForCategory(retryItems, string(aiLibraryCategoryEquipment))

	for _, action := range plan.Advantages {
		if correction, ok := aiResolvedNamedCorrection(advantageItems, advantageUsed, string(aiLibraryCategoryAdvantage), action); ok {
			corrections = append(corrections, correction)
		}
	}
	for _, action := range plan.Disadvantages {
		if correction, ok := aiResolvedNamedCorrection(disadvantageItems, disadvantageUsed, string(aiLibraryCategoryDisadvantage), action); ok {
			corrections = append(corrections, correction)
		}
	}
	for _, action := range plan.Quirks {
		if correction, ok := aiResolvedNamedCorrection(quirkItems, quirkUsed, string(aiLibraryCategoryQuirk), action); ok {
			corrections = append(corrections, correction)
		}
	}
	for _, action := range plan.Skills {
		if correction, ok := aiResolvedSkillCorrection(skillItems, skillUsed, action); ok {
			corrections = append(corrections, correction)
		}
	}
	for _, action := range plan.Spells {
		if correction, ok := aiResolvedSpellCorrection(spellItems, spellUsed, action); ok {
			corrections = append(corrections, correction)
		}
	}
	for _, action := range plan.Equipment {
		if correction, ok := aiResolvedNamedCorrection(equipmentItems, equipmentUsed, string(aiLibraryCategoryEquipment), action); ok {
			corrections = append(corrections, correction)
		}
	}
	return corrections
}

func aiResolvedNamedCorrection(items []aiRetryItem, used []bool, category string, action aiNamedAction) (aiResolvedCorrection, bool) {
	return aiResolvedCorrectionForAction(items, used, category, normalizeAISelectionID(action.ID.String()), normalizeLookupText(action.Name.String()), action.Name.String(), action.Notes.String())
}

func aiResolvedSkillCorrection(items []aiRetryItem, used []bool, action aiSkillAction) (aiResolvedCorrection, bool) {
	return aiResolvedCorrectionForAction(items, used, string(aiLibraryCategorySkill), normalizeAISelectionID(action.ID.String()), normalizeLookupText(action.Name.String()), action.Name.String(), action.Notes.String())
}

func aiResolvedSpellCorrection(items []aiRetryItem, used []bool, action aiSkillAction) (aiResolvedCorrection, bool) {
	return aiResolvedCorrectionForAction(items, used, string(aiLibraryCategorySpell), normalizeAISelectionID(action.ID.String()), normalizeLookupText(action.Name.String()), action.Name.String(), action.Notes.String())
}

func aiResolvedCorrectionForAction(items []aiRetryItem, used []bool, category, id, name, resolvedName, resolvedNotes string) (aiResolvedCorrection, bool) {
	for i, item := range items {
		if used[i] {
			continue
		}
		if !aiCorrectionActionMatchesRetryItem(category, id, name, item) {
			continue
		}
		used[i] = true
		return aiResolvedCorrection{
			Category:   aiCategoryJSONField(category),
			Requested:  aiCorrectionDisplayName(item.Name, item.Notes),
			Resolved:   aiCorrectionDisplayName(resolvedName, resolvedNotes),
			ResolvedID: strings.TrimSpace(id),
		}, true
	}
	return aiResolvedCorrection{}, false
}

func aiCorrectionDisplayName(name, notes string) string {
	name = strings.TrimSpace(name)
	notes = strings.TrimSpace(notes)
	if notes == "" {
		return name
	}
	if name == "" {
		return notes
	}
	nameNorm := normalizeLookupText(name)
	notesNorm := normalizeLookupText(notes)
	if notesNorm != "" && strings.Contains(nameNorm, notesNorm) {
		return name
	}
	return fmt.Sprintf("%s [%s]", name, notes)
}

func aiBuildCorrectionSummary(corrections []aiResolvedCorrection) string {
	if len(corrections) == 0 {
		return ""
	}
	limit := min(len(corrections), 5)
	parts := make([]string, 0, limit)
	for _, correction := range corrections[:limit] {
		parts = append(parts, fmt.Sprintf("%s %q -> %q", aiCategorySingular(correction.Category), correction.Requested, correction.Resolved))
	}
	if len(corrections) > limit {
		return fmt.Sprintf(i18n.Text("Resolver corrections applied: %s; plus %d more."), strings.Join(parts, "; "), len(corrections)-limit)
	}
	return fmt.Sprintf(i18n.Text("Resolver corrections applied: %s."), strings.Join(parts, "; "))
}

func aiLogResolvedCorrections(corrections []aiResolvedCorrection) {
	for _, correction := range corrections {
		fields := []string{
			fmt.Sprintf("category=%s", aiCategoryJSONField(correction.Category)),
			fmt.Sprintf("requested=%q", strings.TrimSpace(correction.Requested)),
			fmt.Sprintf("selected=%q", strings.TrimSpace(correction.Resolved)),
		}
		if strings.TrimSpace(correction.ResolvedID) != "" {
			fields = append(fields, fmt.Sprintf("selected_id=%q", strings.TrimSpace(correction.ResolvedID)))
		}
		aiWriteResolverDebugLog("resolved-correction", fields...)
	}
}

func strconvQuote(text string) string {
	encoded, err := json.Marshal(strings.TrimSpace(text))
	if err != nil {
		return fmt.Sprintf("%q", strings.TrimSpace(text))
	}
	return string(encoded)
}
