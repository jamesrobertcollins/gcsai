package ux

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/generative-ai-go/genai"
	"github.com/richardwilkes/gcs/v5/model/gurps"
)

func TestAIExtractGeminiBuildBriefFromNaturalLanguage(t *testing.T) {
	brief := aiExtractGeminiBuildBrief("Build a 250-point TL8 cyberpunk investigator named Mara Voss with telepathy and signature gear.", aiGeminiBuildBrief{})
	if brief.Name != "Mara Voss" {
		t.Fatalf("expected name Mara Voss, got %q", brief.Name)
	}
	if brief.ConceptBackground != "cyberpunk investigator" {
		t.Fatalf("expected concept background %q, got %q", "cyberpunk investigator", brief.ConceptBackground)
	}
	if brief.SettingGenre != "Cyberpunk" {
		t.Fatalf("expected setting genre Cyberpunk, got %q", brief.SettingGenre)
	}
	if brief.PointTotal != 250 {
		t.Fatalf("expected point total 250, got %d", brief.PointTotal)
	}
	if brief.TechLevel != "8" {
		t.Fatalf("expected tech level 8, got %q", brief.TechLevel)
	}
	if brief.SpecificRequests != "telepathy and signature gear." {
		t.Fatalf("expected specific requests %q, got %q", "telepathy and signature gear.", brief.SpecificRequests)
	}
}

func TestAIPrepareGeminiRequestAsksForMissingFields(t *testing.T) {
	var d aiChatDockable
	prepared := d.prepareGeminiRequest("Build a 250-point TL8 cyberpunk investigator named Mara Voss")
	if !prepared.NeedsUserInput {
		t.Fatal("expected Gemini request to ask for missing fields")
	}
	if d.geminiBuild == nil {
		t.Fatal("expected Gemini build session to remain active while fields are missing")
	}
	if !strings.Contains(prepared.UserFacingMessage, "Setting / Genre") {
		t.Fatalf("expected missing-field prompt to mention Setting / Genre, got %q", prepared.UserFacingMessage)
	}
	if !strings.Contains(prepared.UserFacingMessage, "Specific Mechanics/Requests") {
		t.Fatalf("expected missing-field prompt to mention Specific Mechanics/Requests, got %q", prepared.UserFacingMessage)
	}
}

func TestAIPrepareGeminiRequestBuildsPromptWhenBriefComplete(t *testing.T) {
	var d aiChatDockable
	first := d.prepareGeminiRequest("Build a 250-point TL8 cyberpunk investigator named Mara Voss")
	if !first.NeedsUserInput {
		t.Fatal("expected first Gemini request to collect more fields")
	}
	second := d.prepareGeminiRequest("Setting / Genre: Cyberpunk\nSpecific Mechanics/Requests: Telepathy and signature gear")
	if second.NeedsUserInput {
		t.Fatalf("expected completed Gemini brief to proceed, got %q", second.UserFacingMessage)
	}
	if !second.ExpectJSON {
		t.Fatal("expected completed Gemini build request to require JSON")
	}
	if d.geminiBuild != nil {
		t.Fatal("expected Gemini build session to clear once the brief is complete")
	}
	combined := second.SystemPrompt + "\n" + second.UserPrompt
	checks := []string{
		"Google Gemini operating as a GURPS Fourth Edition character-builder",
		"Completed character brief:",
		"\"Mara Voss\"",
		"\"cyberpunk investigator\"",
		"\"Cyberpunk\"",
		"Point Total: 250",
		"TL 8",
		"\"Telepathy and signature gear\"",
		"Return a single JSON object",
	}
	for _, check := range checks {
		if !strings.Contains(combined, check) {
			t.Fatalf("expected Gemini build prompt to contain %q, got %q", check, combined)
		}
	}
}

func TestAIGeminiModelCandidates(t *testing.T) {
	candidates := aiGeminiModelCandidates("gemini-3.1-pro-preview")
	wantOrder := []string{"gemini-3.1-pro-preview", gurps.FallbackGeminiModel, "gemini-2.5-flash"}
	if len(candidates) != len(wantOrder) {
		t.Fatalf("expected %d candidates, got %d (%#v)", len(wantOrder), len(candidates), candidates)
	}
	for i, want := range wantOrder {
		if candidates[i] != want {
			t.Fatalf("expected candidate %d to be %q, got %q", i, want, candidates[i])
		}
	}
}

func TestAIGeminiModelSupportsGenerateContent(t *testing.T) {
	if !aiGeminiModelSupportsGenerateContent(&genai.ModelInfo{SupportedGenerationMethods: []string{"generateContent"}}) {
		t.Fatal("expected generateContent support to be detected")
	}
	if !aiGeminiModelSupportsGenerateContent(&genai.ModelInfo{SupportedGenerationMethods: []string{"GenerateContent"}}) {
		t.Fatal("expected case-insensitive generateContent support to be detected")
	}
	if aiGeminiModelSupportsGenerateContent(&genai.ModelInfo{SupportedGenerationMethods: []string{"embedContent"}}) {
		t.Fatal("expected non-generateContent model to be rejected")
	}
	if aiGeminiModelSupportsGenerateContent(nil) {
		t.Fatal("expected nil model info to be rejected")
	}
}

func TestAIBuildGeminiRESTGenerateContentRequest(t *testing.T) {
	request := aiBuildGeminiRESTGenerateContentRequest("system", nil, "user", true)
	if request.SystemInstruction == nil || len(request.SystemInstruction.Parts) != 1 || request.SystemInstruction.Parts[0].Text != "system" {
		t.Fatalf("expected system instruction text to be preserved, got %#v", request.SystemInstruction)
	}
	if len(request.Contents) != 1 || len(request.Contents[0].Parts) != 1 || request.Contents[0].Parts[0].Text != "user" {
		t.Fatalf("expected user prompt to be preserved, got %#v", request.Contents)
	}
	if request.GenerationConfig.ResponseMIMEType != aiGeminiJSONResponseMIMEType {
		t.Fatalf("expected JSON response MIME type, got %q", request.GenerationConfig.ResponseMIMEType)
	}
	if request.GenerationConfig.Temperature == nil || *request.GenerationConfig.Temperature != float32(0.2) {
		t.Fatalf("expected JSON request temperature 0.2, got %#v", request.GenerationConfig.Temperature)
	}
}

func TestAIBuildGeminiRESTGenerateContentRequestIncludesHistory(t *testing.T) {
	history := []*genai.Content{
		{Role: "user", Parts: []genai.Part{genai.Text("first question")}},
		{Role: "model", Parts: []genai.Part{genai.Text("first answer")}},
	}
	request := aiBuildGeminiRESTGenerateContentRequest("system", history, "follow-up", false)
	if len(request.Contents) != 3 {
		t.Fatalf("expected 3 content entries, got %#v", request.Contents)
	}
	if request.Contents[0].Role != "user" || request.Contents[0].Parts[0].Text != "first question" {
		t.Fatalf("expected first history entry to be preserved, got %#v", request.Contents[0])
	}
	if request.Contents[1].Role != "model" || request.Contents[1].Parts[0].Text != "first answer" {
		t.Fatalf("expected model history entry to be preserved, got %#v", request.Contents[1])
	}
	if request.Contents[2].Role != "user" || request.Contents[2].Parts[0].Text != "follow-up" {
		t.Fatalf("expected latest prompt to be appended as user content, got %#v", request.Contents[2])
	}
	if request.GenerationConfig.ResponseMIMEType != "" {
		t.Fatalf("expected non-JSON request to omit response MIME type, got %q", request.GenerationConfig.ResponseMIMEType)
	}
	if request.GenerationConfig.Temperature == nil || *request.GenerationConfig.Temperature != float32(0.4) {
		t.Fatalf("expected non-JSON request temperature 0.4, got %#v", request.GenerationConfig.Temperature)
	}
}

func TestAIParseAndExtractGeminiRESTGenerateContentResponse(t *testing.T) {
	body := []byte(`{"candidates":[{"content":{"parts":[{"text":"{\"profile\":{}}"}]}}]}`)
	response, err := aiParseGeminiRESTGenerateContentResponse(body)
	if err != nil {
		t.Fatalf("expected REST response parse to succeed, got %v", err)
	}
	text, err := aiExtractGeminiRESTResponseText(response)
	if err != nil {
		t.Fatalf("expected REST response text extraction to succeed, got %v", err)
	}
	if text != `{"profile":{}}` {
		t.Fatalf("expected extracted text %q, got %q", `{"profile":{}}`, text)
	}
	if _, err := json.Marshal(response); err != nil {
		t.Fatalf("expected parsed response to remain JSON-serializable, got %v", err)
	}
}

func TestAISanitizeGeminiRESTResponseBody(t *testing.T) {
	body := []byte("\xef\xbb\xbf)]}'\n {\"candidates\":[]}")
	got := string(aiSanitizeGeminiRESTResponseBody(body))
	if got != `{"candidates":[]}` {
		t.Fatalf("expected sanitized response body, got %q", got)
	}
}

func TestAIGeminiBuildPromptsIncludeRecommendedTermsAndQuirkGuardrails(t *testing.T) {
	previousCache := globalAILibraryCatalogCache
	globalAILibraryCatalogCache = aiLibraryCatalogCache{}
	t.Cleanup(func() {
		globalAILibraryCatalogCache = previousCache
	})
	libraries := gurps.GlobalSettings().Libraries()
	globalAILibraryCatalogCache.catalog = newTestAILibraryCatalog(
		&aiLibraryCatalogEntry{Category: aiLibraryCategoryAdvantage, ID: "adv-signature", Name: "Signature Gear", DisplayName: "Signature Gear", BaseName: "Signature Gear"},
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-criminology", Name: "Criminology", DisplayName: "Criminology", BaseName: "Criminology"},
	)
	globalAILibraryCatalogCache.signature = aiLibraryCatalogSignature(libraries)

	var d aiChatDockable
	systemPrompt, userPrompt := d.aiGeminiBuildPrompts(aiGeminiBuildBrief{
		Name:              "Mara Voss",
		ConceptBackground: "cyberpunk investigator",
		SettingGenre:      "Cyberpunk",
		PointTotal:        250,
		TechLevel:         "8",
		SpecificRequests:  "signature gear and criminology-focused detective work",
	})
	combined := systemPrompt + "\n" + userPrompt
	checks := []string{
		`Do not invent non-library advantages, disadvantages, quirks, skills, spells, or equipment names.`,
		`Quirks must match real library quirks.`,
		`Recommended Canonical GURPS Terms:`,
		`Advantages: Signature Gear`,
		`Skills: Criminology`,
	}
	for _, check := range checks {
		if !strings.Contains(combined, check) {
			t.Fatalf("expected Gemini build prompt to contain %q, got %q", check, combined)
		}
	}
}
