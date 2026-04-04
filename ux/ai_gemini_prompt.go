package ux

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	"github.com/richardwilkes/gcs/v5/model/fxp"
	"github.com/richardwilkes/gcs/v5/model/gurps"
	"google.golang.org/api/iterator"
)

type aiGeminiBuildBrief struct {
	Name              string
	ConceptBackground string
	SettingGenre      string
	PointTotal        int
	TechLevel         string
	SpecificRequests  string
}

type aiGeminiBuildSession struct {
	Brief aiGeminiBuildBrief
}

type aiPreparedGeminiRequest struct {
	SystemPrompt      string
	UserPrompt        string
	ExpectJSON        bool
	NeedsUserInput    bool
	UserFacingMessage string
}

type aiGeminiRESTPart struct {
	Text string `json:"text,omitempty"`
}

type aiGeminiRESTContent struct {
	Role  string             `json:"role,omitempty"`
	Parts []aiGeminiRESTPart `json:"parts,omitempty"`
}

type aiGeminiRESTGenerationConfig struct {
	ResponseMIMEType string   `json:"responseMimeType,omitempty"`
	Temperature      *float32 `json:"temperature,omitempty"`
}

type aiGeminiRESTGenerateContentRequest struct {
	SystemInstruction *aiGeminiRESTContent         `json:"systemInstruction,omitempty"`
	Contents          []aiGeminiRESTContent        `json:"contents"`
	GenerationConfig  aiGeminiRESTGenerationConfig `json:"generationConfig,omitempty"`
}

type aiGeminiRESTCandidate struct {
	Content aiGeminiRESTContent `json:"content,omitempty"`
}

type aiGeminiRESTError struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Status  string `json:"status,omitempty"`
}

type aiGeminiRESTGenerateContentResponse struct {
	Candidates []aiGeminiRESTCandidate `json:"candidates,omitempty"`
	Error      *aiGeminiRESTError      `json:"error,omitempty"`
}

const aiGeminiJSONResponseMIMEType = "application/json"

const aiGeminiRESTEndpointBase = "https://generativelanguage.googleapis.com/v1beta"

const aiGeminiRESTMaxAttempts = 3

var aiGeminiPreferredModels = []string{
	gurps.DefaultGeminiModel,
	gurps.FallbackGeminiModel,
	"gemini-2.5-flash",
}

var (
	aiGeminiNameFieldPattern             = regexp.MustCompile(`(?im)^\s*name\s*[:=-]\s*(.+?)\s*$`)
	aiGeminiConceptFieldPattern          = regexp.MustCompile(`(?im)^\s*concept(?:\s*&\s*background)?\s*[:=-]\s*(.+?)\s*$`)
	aiGeminiSettingFieldPattern          = regexp.MustCompile(`(?im)^\s*(?:setting(?:\s*/\s*genre)?|genre)\s*[:=-]\s*(.+?)\s*$`)
	aiGeminiSpecificRequestsFieldPattern = regexp.MustCompile(`(?im)^\s*(?:specific\s*(?:mechanics(?:\s*/\s*requests)?|requests)|mechanics|requests?)\s*[:=-]\s*(.+?)\s*$`)
	aiGeminiInlineNamePattern            = regexp.MustCompile(`(?i)\bnamed\s+([^,.;\n]+)`)
	aiGeminiInlineSpecificPattern        = regexp.MustCompile(`(?i)\b(?:with|include|including|focus on|must have|needs?)\s+(.+)$`)
	aiGeminiInlineSettingPattern         = regexp.MustCompile(`(?i)\b(?:setting|genre|campaign)\s*(?:is|:)?\s*([^,.;\n]+)`)
	aiGeminiWhitespacePattern            = regexp.MustCompile(`\s+`)
	aiGeminiGenreHints                   = []struct {
		pattern *regexp.Regexp
		value   string
	}{
		{pattern: regexp.MustCompile(`(?i)\bcyberpunk\b`), value: "Cyberpunk"},
		{pattern: regexp.MustCompile(`(?i)\burban fantasy\b`), value: "Urban Fantasy"},
		{pattern: regexp.MustCompile(`(?i)\bfantasy\b`), value: "Fantasy"},
		{pattern: regexp.MustCompile(`(?i)\bspace opera\b`), value: "Space Opera"},
		{pattern: regexp.MustCompile(`(?i)\bscience fiction\b|\bsci[- ]?fi\b`), value: "Science Fiction"},
		{pattern: regexp.MustCompile(`(?i)\bhorror\b`), value: "Horror"},
		{pattern: regexp.MustCompile(`(?i)\bsupers?\b|\bsuperhero\b`), value: "Supers"},
		{pattern: regexp.MustCompile(`(?i)\bpost[- ]apocalyptic\b`), value: "Post-Apocalyptic"},
		{pattern: regexp.MustCompile(`(?i)\bsteampunk\b`), value: "Steampunk"},
		{pattern: regexp.MustCompile(`(?i)\bwestern\b`), value: "Western"},
		{pattern: regexp.MustCompile(`(?i)\bhistorical\b`), value: "Historical"},
		{pattern: regexp.MustCompile(`(?i)\bmodern\b`), value: "Modern"},
	}
)

func (d *aiChatDockable) prepareGeminiRequest(request string) aiPreparedGeminiRequest {
	request = strings.TrimSpace(request)
	if !aiShouldUseGeminiBuildFlow(request, d.geminiBuild != nil) {
		return aiPreparedGeminiRequest{
			SystemPrompt: d.aiGeminiAssistantSystemPrompt(),
			UserPrompt:   request,
		}
	}
	session := d.geminiBuild
	if session == nil {
		session = &aiGeminiBuildSession{Brief: d.aiDefaultGeminiBuildBrief()}
	}
	missingBefore := aiGeminiBuildBriefMissingFields(session.Brief)
	session.Brief = aiExtractGeminiBuildBrief(request, session.Brief)
	if len(missingBefore) == 1 {
		session.Brief = aiApplyGeminiSingleFieldFallback(session.Brief, missingBefore[0], request)
	}
	missing := aiGeminiBuildBriefMissingFields(session.Brief)
	if len(missing) > 0 {
		d.geminiBuild = session
		return aiPreparedGeminiRequest{
			NeedsUserInput:    true,
			UserFacingMessage: aiBuildGeminiMissingFieldsPrompt(session.Brief, missing),
		}
	}
	d.geminiBuild = nil
	systemPrompt, userPrompt := d.aiGeminiBuildPrompts(session.Brief)
	return aiPreparedGeminiRequest{
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		ExpectJSON:   true,
	}
}

func aiShouldUseGeminiBuildFlow(request string, hasSession bool) bool {
	if hasSession {
		return true
	}
	if strings.TrimSpace(request) == "" {
		return false
	}
	if aiShouldUseDynamicStage1Prompt(request, false) {
		return true
	}
	return aiLooksLikeGeminiBuildBrief(request)
}

func aiLooksLikeGeminiBuildBrief(request string) bool {
	patterns := []*regexp.Regexp{
		aiGeminiNameFieldPattern,
		aiGeminiConceptFieldPattern,
		aiGeminiSettingFieldPattern,
		aiGeminiSpecificRequestsFieldPattern,
	}
	for _, pattern := range patterns {
		if pattern.MatchString(request) {
			return true
		}
	}
	return false
}

func (d *aiChatDockable) aiDefaultGeminiBuildBrief() aiGeminiBuildBrief {
	var brief aiGeminiBuildBrief
	if sheet := d.activeOrOpenSheet(); sheet != nil && sheet.entity != nil {
		entity := sheet.entity
		brief.Name = strings.TrimSpace(entity.Profile.Name)
		brief.ConceptBackground = strings.TrimSpace(entity.Profile.Title)
		if total := fxp.AsInteger[int](entity.TotalPoints); total > 0 {
			brief.PointTotal = total
		}
		brief.TechLevel = normalizeAIRequestTechLevel(entity.Profile.TechLevel)
	}
	return brief
}

func aiExtractGeminiBuildBrief(request string, defaults aiGeminiBuildBrief) aiGeminiBuildBrief {
	brief := defaults
	if value := aiExtractGeminiField(request, aiGeminiNameFieldPattern); value != "" {
		brief.Name = value
	} else if brief.Name == "" {
		brief.Name = aiExtractGeminiInlineName(request)
	}
	if value := aiExtractGeminiField(request, aiGeminiConceptFieldPattern); value != "" {
		brief.ConceptBackground = value
	} else if brief.ConceptBackground == "" {
		brief.ConceptBackground = aiExtractGeminiConceptBackground(request)
	}
	if value := aiExtractGeminiField(request, aiGeminiSettingFieldPattern); value != "" {
		brief.SettingGenre = value
	} else if brief.SettingGenre == "" {
		brief.SettingGenre = aiExtractGeminiSettingGenre(request)
	}
	if points := aiExtractTotalCP(request); points > 0 {
		brief.PointTotal = points
	}
	if techLevel := normalizeAIRequestTechLevel(aiExtractTechLevel(request)); techLevel != "" {
		brief.TechLevel = techLevel
	}
	if value := aiExtractGeminiField(request, aiGeminiSpecificRequestsFieldPattern); value != "" {
		brief.SpecificRequests = value
	} else if brief.SpecificRequests == "" {
		brief.SpecificRequests = aiExtractGeminiSpecificRequests(request)
	}
	brief.Name = aiNormalizeGeminiBriefText(brief.Name)
	brief.ConceptBackground = aiNormalizeGeminiBriefText(brief.ConceptBackground)
	brief.SettingGenre = aiNormalizeGeminiBriefText(brief.SettingGenre)
	brief.TechLevel = normalizeAIRequestTechLevel(brief.TechLevel)
	brief.SpecificRequests = aiNormalizeGeminiBriefText(brief.SpecificRequests)
	return brief
}

func aiExtractGeminiField(request string, pattern *regexp.Regexp) string {
	match := pattern.FindStringSubmatch(request)
	if len(match) != 2 {
		return ""
	}
	return aiNormalizeGeminiBriefText(match[1])
}

func aiExtractGeminiInlineName(request string) string {
	match := aiGeminiInlineNamePattern.FindStringSubmatch(request)
	if len(match) != 2 {
		return ""
	}
	return aiNormalizeGeminiInlineValue(match[1])
}

func aiExtractGeminiSettingGenre(request string) string {
	if match := aiGeminiInlineSettingPattern.FindStringSubmatch(request); len(match) == 2 {
		return aiNormalizeGeminiInlineValue(match[1])
	}
	for _, hint := range aiGeminiGenreHints {
		if hint.pattern.MatchString(request) {
			return hint.value
		}
	}
	return ""
}

func aiExtractGeminiSpecificRequests(request string) string {
	match := aiGeminiInlineSpecificPattern.FindStringSubmatch(request)
	if len(match) != 2 {
		return ""
	}
	return aiNormalizeGeminiBriefText(match[1])
}

func aiExtractGeminiConceptBackground(request string) string {
	concept := request
	for _, pattern := range aiStage1ConstraintCleanup {
		concept = pattern.ReplaceAllString(concept, " ")
	}
	concept = aiGeminiNameFieldPattern.ReplaceAllString(concept, " ")
	concept = aiGeminiConceptFieldPattern.ReplaceAllString(concept, " ")
	concept = aiGeminiSettingFieldPattern.ReplaceAllString(concept, " ")
	concept = aiGeminiSpecificRequestsFieldPattern.ReplaceAllString(concept, " ")
	concept = aiGeminiInlineNamePattern.ReplaceAllString(concept, " ")
	concept = aiGeminiInlineSettingPattern.ReplaceAllString(concept, " ")
	concept = aiGeminiInlineSpecificPattern.ReplaceAllString(concept, " ")
	concept = aiStage1LeadInPattern.ReplaceAllString(concept, "")
	concept = aiStage1CharacterWordPattern.ReplaceAllString(concept, " ")
	concept = aiGeminiWhitespacePattern.ReplaceAllString(concept, " ")
	concept = strings.TrimSpace(strings.Trim(concept, "[](){}.,;:-"))
	return concept
}

func aiNormalizeGeminiInlineValue(value string) string {
	value = strings.TrimSpace(value)
	cutoffs := []string{" with ", " who ", " from ", " for ", " in ", " on ", " at "}
	lower := strings.ToLower(value)
	for _, cutoff := range cutoffs {
		if index := strings.Index(lower, cutoff); index >= 0 {
			value = value[:index]
			break
		}
	}
	return aiNormalizeGeminiBriefText(value)
}

func aiNormalizeGeminiBriefText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.TrimSpace(aiGeminiWhitespacePattern.ReplaceAllString(value, " "))
}

func aiGeminiBuildBriefMissingFields(brief aiGeminiBuildBrief) []string {
	missing := make([]string, 0, 6)
	if strings.TrimSpace(brief.Name) == "" {
		missing = append(missing, "Name")
	}
	if strings.TrimSpace(brief.ConceptBackground) == "" {
		missing = append(missing, "Concept & Background")
	}
	if strings.TrimSpace(brief.SettingGenre) == "" {
		missing = append(missing, "Setting / Genre")
	}
	if brief.PointTotal <= 0 {
		missing = append(missing, "Point Total")
	}
	if strings.TrimSpace(brief.TechLevel) == "" {
		missing = append(missing, "Tech Level")
	}
	if strings.TrimSpace(brief.SpecificRequests) == "" {
		missing = append(missing, "Specific Mechanics/Requests")
	}
	return missing
}

func aiApplyGeminiSingleFieldFallback(brief aiGeminiBuildBrief, field, request string) aiGeminiBuildBrief {
	value := aiNormalizeGeminiBriefText(request)
	if value == "" {
		return brief
	}
	switch field {
	case "Name":
		if brief.Name == "" {
			brief.Name = value
		}
	case "Concept & Background":
		if brief.ConceptBackground == "" {
			brief.ConceptBackground = value
		}
	case "Setting / Genre":
		if brief.SettingGenre == "" {
			brief.SettingGenre = value
		}
	case "Point Total":
		if brief.PointTotal == 0 {
			if points := aiExtractTotalCP(value); points > 0 {
				brief.PointTotal = points
			} else if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
				brief.PointTotal = parsed
			}
		}
	case "Tech Level":
		if brief.TechLevel == "" {
			brief.TechLevel = normalizeAIRequestTechLevel(value)
		}
	case "Specific Mechanics/Requests":
		if brief.SpecificRequests == "" {
			brief.SpecificRequests = value
		}
	}
	return brief
}

func aiBuildGeminiMissingFieldsPrompt(brief aiGeminiBuildBrief, missing []string) string {
	var builder strings.Builder
	builder.WriteString("I need the remaining Gemini character brief fields before I generate the character JSON.\n")
	builder.WriteString("Missing: ")
	builder.WriteString(strings.Join(missing, ", "))
	builder.WriteString(".\n")
	if current := aiFormatGeminiBuildBrief(brief); current != "" {
		builder.WriteString("Current brief:\n")
		builder.WriteString(current)
		builder.WriteString("\n")
	}
	if len(missing) == 1 {
		builder.WriteString("Reply with ")
		builder.WriteString(missing[0])
		builder.WriteString(".")
		return builder.String()
	}
	builder.WriteString("Reply with the missing fields using labels such as:\n")
	for _, field := range missing {
		builder.WriteString(field)
		builder.WriteString(": ...\n")
	}
	return strings.TrimSpace(builder.String())
}

func aiFormatGeminiBuildBrief(brief aiGeminiBuildBrief) string {
	entries := make([]string, 0, 6)
	if brief.Name != "" {
		entries = append(entries, fmt.Sprintf("- Name: %s", brief.Name))
	}
	if brief.ConceptBackground != "" {
		entries = append(entries, fmt.Sprintf("- Concept & Background: %s", brief.ConceptBackground))
	}
	if brief.SettingGenre != "" {
		entries = append(entries, fmt.Sprintf("- Setting / Genre: %s", brief.SettingGenre))
	}
	if brief.PointTotal > 0 {
		entries = append(entries, fmt.Sprintf("- Point Total: %d", brief.PointTotal))
	}
	if brief.TechLevel != "" {
		entries = append(entries, fmt.Sprintf("- Tech Level: %s", brief.TechLevel))
	}
	if brief.SpecificRequests != "" {
		entries = append(entries, fmt.Sprintf("- Specific Mechanics/Requests: %s", brief.SpecificRequests))
	}
	return strings.Join(entries, "\n")
}

func (d *aiChatDockable) aiGeminiAssistantSystemPrompt() string {
	summary := d.currentCharacterSummary()
	return strings.TrimSpace(fmt.Sprintf(`You are Google Gemini acting as a GURPS Fourth Edition character-sheet assistant.
Answer rules questions concisely and propose concrete character-sheet updates when the user asks for changes.
The application will resolve your suggested advantages, disadvantages, quirks, skills, traits, and equipment against the local GCS library after you respond.
Do not invent database ids. Leave the "id" field empty unless you are certain.
Use canonical GURPS Fourth Edition names instead of descriptive paraphrases.
If a fixed specialization is part of the canonical library name, include it in "name".
If an item needs a user-defined subject, place, profession, specialty, or other nameable value, put only that value in "notes" and keep "name" focused on the base item.
Use "description" for lore, behavior, magical effects, and special handling notes. Do not put that material in "notes".
Do not invent non-library advantages, disadvantages, skills, or equipment names.
If the concept includes a magical, signature, or supernatural item, represent it through canonical GURPS mechanics such as Signature Gear, Innate Attack, Ally, Blessed, Patron, or Striking ST, and put the lore and special behavior in "description".
Only include an equipment entry when it matches a real library item; otherwise keep the special concept on the trait side.
If you include JSON, return exactly one top-level JSON object and nothing else.

Current character sheet context:
%s`, summary))
}

func (d *aiChatDockable) aiGeminiCorrectionSystemPrompt() string {
	return strings.TrimSpace(`You are Google Gemini acting as a GURPS Fourth Edition library-resolution assistant.
Return exactly one top-level JSON object and nothing else.
Only include corrected entries for the unresolved items in the user prompt.
Use only the candidate ids and candidate names shown there.
Preserve points, quantity, notes, and description unless the selected candidate requires a different nameable value.
If no valid correction exists for an item, omit it.`)
}

func (d *aiChatDockable) aiGeminiBuildPrompts(brief aiGeminiBuildBrief) (systemPrompt, userPrompt string) {
	summary := d.currentCharacterSummary()
	disadvantageLimit := aiDefaultDisadvantageLimit(brief.PointTotal)
	systemPrompt = strings.TrimSpace(`You are Google Gemini operating as a GURPS Fourth Edition character-builder.
The application has already collected a complete build brief from the user.
Generate a substantial first-pass character build immediately. Do not return a partial draft.
Return exactly one top-level JSON object and nothing else.
The application will resolve your suggested advantages, disadvantages, quirks, skills, traits, and equipment against the local GCS library after you respond.
Do not invent database ids. Leave the "id" field empty unless you are certain.
Use canonical GURPS Fourth Edition names instead of descriptive paraphrases.
If a fixed specialization is part of the canonical library name, include it in "name".
If an item needs a user-defined subject, place, profession, specialty, or other nameable value, put only that value in "notes" and keep "name" focused on the base item.
Use "description" for lore, behavior, magical effects, and special handling notes. Do not put that material in "notes".
Do not invent non-library advantages, disadvantages, skills, or equipment names.
If the concept includes a magical, signature, or supernatural item, represent it through canonical GURPS mechanics such as Signature Gear, Innate Attack, Ally, Blessed, Patron, or Striking ST, and put the lore and special behavior in "description".
Only include an equipment entry when it matches a real library item; otherwise keep the special concept on the trait side.
Populate profile fields when the brief provides them, especially name, title, and tech_level.
Spend the requested point total and set spend_all_cp to true once the build is complete.`)
	userPrompt = strings.TrimSpace(fmt.Sprintf(`Completed character brief:
- Name: %s
- Concept & Background: %s
- Setting / Genre: %s
- Point Total: %d
- Tech Level: TL %s
- Specific Mechanics/Requests: %s

Current character sheet context:
%s

Budget guidance:
- Target exactly %d CP.
- Use up to %d points in disadvantages when that supports the concept.
- Include exactly 5 quirks when appropriate.
- Spend roughly 40-50%% of the budget on attributes and secondary characteristics.
- Spend roughly 15-25%% on concept-fitting advantages.
- Spend the remaining budget on a broad, realistic skill list rather than only a few headline skills.
- Include relevant starting equipment when it resolves to real library entries.

Return a single JSON object that uses the applicable keys from:
- profile
- attributes
- advantages
- disadvantages
- quirks
- skills
- equipment
- spend_all_cp

Make reasonable assumptions and complete the build in one pass.`, strconvQuote(brief.Name), strconvQuote(brief.ConceptBackground), strconvQuote(brief.SettingGenre), brief.PointTotal, brief.TechLevel, strconvQuote(brief.SpecificRequests), summary, brief.PointTotal, disadvantageLimit))
	return systemPrompt, userPrompt
}

func (d *aiChatDockable) sendGeminiRequest(ctx context.Context, modelName, systemPrompt, prompt string, expectJSON bool) (string, error) {
	var history []*genai.Content
	if !expectJSON {
		history = d.chatHistory
	}
	return d.sendGeminiRESTGenerateContent(ctx, modelName, systemPrompt, history, prompt, expectJSON)
}

func (d *aiChatDockable) sendGeminiRESTGenerateContent(ctx context.Context, modelName, systemPrompt string, history []*genai.Content, prompt string, expectJSON bool) (string, error) {
	requestBody := aiBuildGeminiRESTGenerateContentRequest(systemPrompt, history, prompt, expectJSON)
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return "", err
	}
	requestURL := aiGeminiRESTGenerateContentURL(modelName, gurps.GlobalSettings().AI.GeminiAPIKey)
	var lastErr error
	for attempt := 1; attempt <= aiGeminiRESTMaxAttempts; attempt++ {
		responseText, callErr := aiDoGeminiRESTGenerateContent(ctx, requestURL, payload)
		if callErr == nil {
			return responseText, nil
		}
		lastErr = callErr
		if attempt == aiGeminiRESTMaxAttempts || !aiGeminiShouldRetryRESTError(callErr) {
			break
		}
		time.Sleep(time.Duration(attempt) * 250 * time.Millisecond)
	}
	return "", lastErr
}

func aiDoGeminiRESTGenerateContent(ctx context.Context, requestURL string, payload []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	body = aiSanitizeGeminiRESTResponseBody(body)
	parsed, parseErr := aiParseGeminiRESTGenerateContentResponse(body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if parseErr == nil && parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
			return "", fmt.Errorf("Gemini HTTP %d: %s", resp.StatusCode, strings.TrimSpace(parsed.Error.Message))
		}
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return "", fmt.Errorf("Gemini HTTP %d: %s", resp.StatusCode, message)
	}
	if parseErr != nil {
		bodyPreview := strings.TrimSpace(string(body))
		if len(bodyPreview) > 300 {
			bodyPreview = bodyPreview[:300]
		}
		return "", fmt.Errorf("unable to parse Gemini response: %w; body=%q", parseErr, bodyPreview)
	}
	return aiExtractGeminiRESTResponseText(parsed)
}

func aiBuildGeminiRESTGenerateContentRequest(systemPrompt string, history []*genai.Content, prompt string, expectJSON bool) aiGeminiRESTGenerateContentRequest {
	contents := aiGeminiRESTContentsFromHistory(history)
	contents = append(contents, aiGeminiRESTContent{Role: "user", Parts: []aiGeminiRESTPart{{Text: prompt}}})
	request := aiGeminiRESTGenerateContentRequest{
		SystemInstruction: &aiGeminiRESTContent{Role: "user", Parts: []aiGeminiRESTPart{{Text: systemPrompt}}},
		Contents:          contents,
		GenerationConfig:  aiGeminiRESTGenerationConfig{Temperature: aiGeminiTemperature(expectJSON)},
	}
	if expectJSON {
		request.GenerationConfig.ResponseMIMEType = aiGeminiJSONResponseMIMEType
	}
	return request
}

func aiGeminiTemperature(expectJSON bool) *float32 {
	value := float32(0.4)
	if expectJSON {
		value = 0.2
	}
	return &value
}

func aiGeminiRESTContentsFromHistory(history []*genai.Content) []aiGeminiRESTContent {
	contents := make([]aiGeminiRESTContent, 0, len(history))
	for _, entry := range history {
		content := aiGeminiRESTContentFromHistoryEntry(entry)
		if len(content.Parts) == 0 {
			continue
		}
		contents = append(contents, content)
	}
	return contents
}

func aiGeminiRESTContentFromHistoryEntry(entry *genai.Content) aiGeminiRESTContent {
	if entry == nil {
		return aiGeminiRESTContent{}
	}
	text := strings.TrimSpace(aiMarshalLocalContent(entry))
	if text == "" {
		return aiGeminiRESTContent{}
	}
	role := strings.ToLower(strings.TrimSpace(entry.Role))
	if role != "model" {
		role = "user"
	}
	return aiGeminiRESTContent{Role: role, Parts: []aiGeminiRESTPart{{Text: text}}}
}

func aiGeminiRESTGenerateContentURL(modelName, apiKey string) string {
	endpoint := &url.URL{
		Scheme: "https",
		Host:   "generativelanguage.googleapis.com",
		Path:   "/v1beta/models/" + url.PathEscape(modelName) + ":generateContent",
	}
	query := endpoint.Query()
	query.Set("key", apiKey)
	endpoint.RawQuery = query.Encode()
	return endpoint.String()
}

func aiGeminiShouldRetryRESTError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(message, "http 429") || strings.Contains(message, "http 500") || strings.Contains(message, "http 502") || strings.Contains(message, "http 503") || strings.Contains(message, "http 504") {
		return true
	}
	if strings.Contains(message, "forcibly closed") || strings.Contains(message, "connection reset") || strings.Contains(message, "unexpected eof") || strings.Contains(message, "server misbehaving") || strings.Contains(message, "broken pipe") {
		return true
	}
	return false
}

func aiSanitizeGeminiRESTResponseBody(body []byte) []byte {
	trimmed := bytes.TrimSpace(body)
	trimmed = bytes.TrimPrefix(trimmed, []byte("\xef\xbb\xbf"))
	if bytes.HasPrefix(trimmed, []byte(")]}'")) {
		trimmed = bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte(")]}'")))
	}
	return trimmed
}

func aiParseGeminiRESTGenerateContentResponse(body []byte) (aiGeminiRESTGenerateContentResponse, error) {
	var response aiGeminiRESTGenerateContentResponse
	err := json.Unmarshal(body, &response)
	return response, err
}

func aiExtractGeminiRESTResponseText(response aiGeminiRESTGenerateContentResponse) (string, error) {
	if response.Error != nil && strings.TrimSpace(response.Error.Message) != "" {
		return "", fmt.Errorf("%s", strings.TrimSpace(response.Error.Message))
	}
	var builder strings.Builder
	for _, candidate := range response.Candidates {
		for _, part := range candidate.Content.Parts {
			if strings.TrimSpace(part.Text) != "" {
				builder.WriteString(part.Text)
			}
		}
	}
	if builder.Len() == 0 {
		return "", fmt.Errorf("Gemini returned no text candidates")
	}
	return builder.String(), nil
}

func (d *aiChatDockable) resolveGeminiModelName(ctx context.Context, client *genai.Client) (resolvedModel, warning string) {
	requestedModel := gurps.GlobalSettings().AI.EffectiveGeminiModel()
	availableModels, err := aiAvailableGeminiGenerateContentModels(ctx, client)
	if err != nil || len(availableModels) == 0 {
		return requestedModel, ""
	}
	for _, candidate := range aiGeminiModelCandidates(requestedModel) {
		if _, ok := availableModels[candidate]; ok {
			if candidate != requestedModel {
				return candidate, fmt.Sprintf("Configured Gemini model %q is unavailable on this API endpoint. Using %q instead.", requestedModel, candidate)
			}
			return candidate, ""
		}
	}
	return requestedModel, ""
}

func aiGeminiModelCandidates(requestedModel string) []string {
	seen := make(map[string]struct{})
	add := func(list *[]string, model string) {
		model = strings.TrimSpace(model)
		if model == "" {
			return
		}
		if _, ok := seen[model]; ok {
			return
		}
		seen[model] = struct{}{}
		*list = append(*list, model)
	}
	var candidates []string
	add(&candidates, requestedModel)
	for _, model := range aiGeminiPreferredModels {
		add(&candidates, model)
	}
	return candidates
}

func aiAvailableGeminiGenerateContentModels(ctx context.Context, client *genai.Client) (map[string]struct{}, error) {
	models := make(map[string]struct{})
	iter := client.ListModels(ctx)
	for {
		info, err := iter.Next()
		if err == iterator.Done {
			return models, nil
		}
		if err != nil {
			return nil, err
		}
		if info == nil || !aiGeminiModelSupportsGenerateContent(info) {
			continue
		}
		if model := strings.TrimPrefix(strings.TrimSpace(info.Name), "models/"); model != "" {
			models[model] = struct{}{}
		}
		if model := strings.TrimSpace(info.BaseModelID); model != "" {
			models[model] = struct{}{}
		}
	}
}

func aiGeminiModelSupportsGenerateContent(info *genai.ModelInfo) bool {
	if info == nil {
		return false
	}
	for _, method := range info.SupportedGenerationMethods {
		if strings.EqualFold(strings.TrimSpace(method), "generateContent") {
			return true
		}
	}
	return false
}

func extractGeminiResponseText(resp *genai.GenerateContentResponse) string {
	if resp == nil {
		return ""
	}
	var builder strings.Builder
	for _, candidate := range resp.Candidates {
		if candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			if text, ok := part.(genai.Text); ok {
				builder.WriteString(string(text))
			}
		}
	}
	return builder.String()
}
