package ux

import (
	"strings"
	"testing"
)

func TestAIExtractCharacterRequestParams(t *testing.T) {
	defaults := aiCharacterRequestParams{
		TotalCP:           150,
		TechLevel:         "3",
		Concept:           "Fallback Concept",
		DisadvantageLimit: 50,
	}
	got := aiExtractCharacterRequestParams("Build a 250-point TL8 cyberpunk investigator with up to 40 points in disadvantages.", defaults)
	if got.TotalCP != 250 {
		t.Fatalf("expected TotalCP 250, got %d", got.TotalCP)
	}
	if got.TechLevel != "8" {
		t.Fatalf("expected TechLevel 8, got %q", got.TechLevel)
	}
	if got.DisadvantageLimit != 40 {
		t.Fatalf("expected DisadvantageLimit 40, got %d", got.DisadvantageLimit)
	}
	if got.Concept != "cyberpunk investigator" {
		t.Fatalf("expected concept %q, got %q", "cyberpunk investigator", got.Concept)
	}
}

func TestAIExtractCharacterRequestParamsKeepsDefaultsWhenMissing(t *testing.T) {
	defaults := aiCharacterRequestParams{
		TotalCP:           125,
		TechLevel:         "4",
		Concept:           "Fallback Concept",
		DisadvantageLimit: 30,
	}
	got := aiExtractCharacterRequestParams("Create a wandering knight for GURPS", defaults)
	if got.TotalCP != 125 {
		t.Fatalf("expected TotalCP 125, got %d", got.TotalCP)
	}
	if got.TechLevel != "4" {
		t.Fatalf("expected TechLevel 4, got %q", got.TechLevel)
	}
	if got.DisadvantageLimit != 30 {
		t.Fatalf("expected DisadvantageLimit 30, got %d", got.DisadvantageLimit)
	}
	if got.Concept != "wandering knight" {
		t.Fatalf("expected concept %q, got %q", "wandering knight", got.Concept)
	}
}

func TestAIExtractTotalCPIgnoresDisadvantagePoints(t *testing.T) {
	got := aiExtractTotalCP("Use up to 40 points in disadvantages on a 25-point child prodigy.")
	if got != 25 {
		t.Fatalf("expected TotalCP 25, got %d", got)
	}
}

func TestAIRenderStage1SystemPrompt(t *testing.T) {
	prompt := aiRenderStage1SystemPrompt(aiStage1SystemPromptData{
		aiCharacterRequestParams: aiCharacterRequestParams{
			TotalCP:           250,
			TechLevel:         "8",
			Concept:           "cyberpunk investigator",
			DisadvantageLimit: 40,
		},
		Summary: "No active GURPS sheet is open.",
	})
	checks := []string{
		"[cyberpunk investigator]",
		"exactly 250 Character Points (CP)",
		"TL 8",
		"up to 40 points in disadvantages",
		"No active GURPS sheet is open.",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Fatalf("expected prompt to contain %q", check)
		}
	}
}

func TestAIShouldUseDynamicStage1Prompt(t *testing.T) {
	if !aiShouldUseDynamicStage1Prompt("Build a 150-point swashbuckler.", false) {
		t.Fatal("expected first build request to use dynamic stage-1 prompt")
	}
	if aiShouldUseDynamicStage1Prompt("Build a 150-point swashbuckler.", true) {
		t.Fatal("expected follow-up requests to stay on the generic prompt")
	}
	if aiShouldUseDynamicStage1Prompt("How does TL8 equipment legality work?", false) {
		t.Fatal("expected general rules questions to stay on the generic prompt")
	}
}

func TestAIShouldUseBuildContinuationPrompt(t *testing.T) {
	if !aiShouldUseBuildContinuationPrompt("add advantages") {
		t.Fatal("expected short follow-up category request to use build continuation prompt")
	}
	if !aiShouldUseBuildContinuationPrompt("spend the remaining points on skills and gear") {
		t.Fatal("expected remaining-points request to use build continuation prompt")
	}
	if aiShouldUseBuildContinuationPrompt("How much does Combat Reflexes cost?") {
		t.Fatal("expected general rules question to avoid build continuation prompt")
	}
}

func TestAIActionPlanNeedsCharacterBuildCompletion(t *testing.T) {
	if !aiActionPlanNeedsCharacterBuildCompletion(aiActionPlan{
		Profile:    &aiProfileAction{Name: aiFlexibleString("Thomas Smith"), Title: aiFlexibleString("Mechanic")},
		SpendAllCP: true,
	}) {
		t.Fatal("expected profile-only build plan to require completion")
	}

	if aiActionPlanNeedsCharacterBuildCompletion(aiActionPlan{
		Profile:       &aiProfileAction{Name: aiFlexibleString("Thomas Smith"), Title: aiFlexibleString("Mechanic")},
		Attributes:    []aiAttributeAction{{ID: aiFlexibleString("DX"), Value: aiFlexibleString("12")}, {ID: aiFlexibleString("IQ"), Value: aiFlexibleString("11")}},
		Advantages:    []aiNamedAction{{Name: aiFlexibleString("Combat Reflexes"), Points: aiFlexibleString("15")}},
		Disadvantages: []aiNamedAction{{Name: aiFlexibleString("Overconfidence"), Points: aiFlexibleString("-5")}},
		Quirks:        []aiNamedAction{{Name: aiFlexibleString("Keeps tools immaculate"), Points: aiFlexibleString("-1")}},
		Skills:        []aiSkillAction{{Name: aiFlexibleString("Mechanic (Vehicles)"), Points: aiFlexibleString("12")}, {Name: aiFlexibleString("Brawling"), Points: aiFlexibleString("4")}},
		Equipment:     []aiNamedAction{{Name: aiFlexibleString("Tool Kit"), Quantity: aiFlexibleInt(1)}},
		SpendAllCP:    true,
	}) {
		t.Fatal("expected substantial build plan to be treated as complete enough")
	}
}

func TestAIBuildCharacterExpansionPrompt(t *testing.T) {
	prompt := aiBuildCharacterExpansionPrompt(
		"Build a 150-point TL8 mechanic.",
		aiCharacterRequestParams{TotalCP: 150, TechLevel: "8", Concept: "mechanic", DisadvantageLimit: 50},
		aiActionPlan{Profile: &aiProfileAction{Title: aiFlexibleString("Mechanic")}, SpendAllCP: true},
	)
	checks := []string{
		"too incomplete for an initial GURPS 4e character build",
		"Do not wait for the user to ask for more details",
		"Budget: 150 CP | TL 8 | Concept: mechanic | Disadvantage limit: 50",
		"attributes",
		"advantages/disadvantages/quirks",
		"skills",
		"Return ONLY the JSON object.",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Fatalf("expected prompt to contain %q, got %q", check, prompt)
		}
	}
}

func TestAIBuildContinuationUserPrompt(t *testing.T) {
	prompt := aiBuildContinuationUserPrompt("add advantages", aiCharacterRequestParams{TotalCP: 150, TechLevel: "8", Concept: "mechanic"})
	checks := []string{
		"Continue the same GURPS 4e character build.",
		"Latest user instruction: \"add advantages\"",
		"Target budget remains 150 CP at TL 8 for concept mechanic.",
		"Return ONLY incremental JSON updates for this turn.",
		"Do not repeat items already on the character sheet unless you are changing them.",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Fatalf("expected prompt to contain %q, got %q", check, prompt)
		}
	}
}

func TestAIBuildLocalPhase1Prompts(t *testing.T) {
	systemPrompt, userPrompt := aiBuildLocalPhase1Prompts(
		"Build a 200-point TL8 detective.",
		aiCharacterRequestParams{TotalCP: 200, TechLevel: "8", Concept: "detective", DisadvantageLimit: 50},
		"No active GURPS sheet is open.",
		"Recommended Canonical GURPS Terms:\n- Advantages: Signature Gear, Blessed",
	)
	checks := []string{
		"deterministic GURPS 4e JSON generation function",
		"No active GURPS sheet is open.",
		"Phase 1: The Core Chassis.",
		"Recommended Canonical GURPS Terms:",
		"Strongly prefer selecting items from this list when they fit the concept.",
		"ONLY these JSON fields",
		"profile",
		"attributes",
		"advantages",
		"disadvantages",
		"quirks",
		"include a credible profile block",
		"40-50%",
		"15-25%",
		"up to 50 points in disadvantages",
		"Use \"description\" for lore, behavior, magical effects, and special handling notes.",
	}
	combined := systemPrompt + "\n" + userPrompt
	for _, check := range checks {
		if !strings.Contains(combined, check) {
			t.Fatalf("expected phase-1 prompts to contain %q, got %q", check, combined)
		}
	}
}

func TestAIBuildLocalPhase2Prompts(t *testing.T) {
	systemPrompt, userPrompt := aiBuildLocalPhase2Prompts(
		"Build a 200-point TL8 detective.",
		aiCharacterRequestParams{TotalCP: 200, TechLevel: "8", Concept: "detective", DisadvantageLimit: 50},
		73,
		"Current character concept: detective",
		"Recommended Canonical GURPS Terms:\n- Skills: Criminology, Observation\n- Equipment: Lockpicks",
	)
	checks := []string{
		"deterministic GURPS 4e JSON generation function",
		"Current character concept: detective",
		"Phase 2: The Professional Package.",
		"Exactly 73 CP remain after Phase 1.",
		"Recommended Canonical GURPS Terms:",
		"Strongly prefer selecting items from this list when they fit the concept.",
		"ONLY these JSON fields",
		"skills",
		"equipment",
		"Avoid padding the build with multiple near-duplicate skill-family variants",
		"snap them to valid GURPS 4e point costs",
		"Only include an equipment entry when it matches a real library item",
	}
	combined := systemPrompt + "\n" + userPrompt
	for _, check := range checks {
		if !strings.Contains(combined, check) {
			t.Fatalf("expected phase-2 prompts to contain %q, got %q", check, combined)
		}
	}
}

func TestAIPrepareAIRequestUsesBuildContinuationSession(t *testing.T) {
	var d aiChatDockable
	d.buildSession = &aiBuildSessionContext{Params: aiCharacterRequestParams{TotalCP: 150, TechLevel: "8", Concept: "mechanic", DisadvantageLimit: 50}}
	prepared := d.prepareAIRequest("add advantages")
	if !strings.Contains(prepared.SystemPrompt, "continuing an in-progress GURPS Fourth Edition character build") {
		t.Fatalf("expected continuation system prompt, got %q", prepared.SystemPrompt)
	}
	if !strings.Contains(prepared.SystemPrompt, "40-50%") {
		t.Fatalf("expected continuation prompt to preserve budget guidance, got %q", prepared.SystemPrompt)
	}
	if !strings.Contains(prepared.UserPrompt, "Return ONLY incremental JSON updates") {
		t.Fatalf("expected rewritten continuation user prompt, got %q", prepared.UserPrompt)
	}
	if prepared.IsInitialBuild {
		t.Fatal("expected continuation request to avoid initial build mode")
	}
}
