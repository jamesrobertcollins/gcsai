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
		StartingWealth:    1000,
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
	if got.StartingWealth != 20000 {
		t.Fatalf("expected default TL8 starting wealth 20000, got %d", got.StartingWealth)
	}
}

func TestAIExtractCharacterRequestParamsKeepsDefaultsWhenMissing(t *testing.T) {
	defaults := aiCharacterRequestParams{
		TotalCP:           125,
		TechLevel:         "4",
		Concept:           "Fallback Concept",
		StartingWealth:    2000,
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
	if got.StartingWealth != 2000 {
		t.Fatalf("expected StartingWealth 2000, got %d", got.StartingWealth)
	}
}

func TestAIExtractCharacterRequestParamsStartingWealth(t *testing.T) {
	defaults := aiCharacterRequestParams{TotalCP: 150, TechLevel: "8", Concept: "Fallback", StartingWealth: 20000, DisadvantageLimit: 50}
	got := aiExtractCharacterRequestParams("Build a TL8 detective with $75,000 starting wealth.", defaults)
	if got.StartingWealth != 75000 {
		t.Fatalf("expected StartingWealth 75000, got %d", got.StartingWealth)
	}
	if got.Concept != "detective" {
		t.Fatalf("expected concept %q, got %q", "detective", got.Concept)
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

func TestAIBuildContinuationSystemPromptIncludesStateContract(t *testing.T) {
	var d aiChatDockable
	prompt := d.aiBuildContinuationSystemPrompt("add advantages", aiCharacterRequestParams{TotalCP: 150, TechLevel: "8", Concept: "mechanic", DisadvantageLimit: 50})
	checks := []string{
		"CURRENT STATE: CONTINUATION_UPDATE",
		"TASK: Apply the latest user instruction as an incremental update to the in-progress character sheet.",
		"ALLOWED FIELDS: profile, attributes, advantages, disadvantages, quirks, skills, spells, equipment, spend_all_cp.",
		"STRICT CONSTRAINT: Return only changed fields for this turn.",
		"continuing an in-progress GURPS Fourth Edition character build",
		"Return only the new or changed JSON needed for this turn.",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Fatalf("expected continuation system prompt to contain %q, got %q", check, prompt)
		}
	}
}

func TestAILocalBaselineGatheringSystemPrompt(t *testing.T) {
	prompt := aiLocalBaselineGatheringSystemPrompt(aiDraftProfile{
		CharacterConcept: aiFlexibleString("Noir Detective"),
		TechLevel:        aiFlexibleString("8"),
		CPLimit:          aiFlexibleString("150"),
	})
	checks := []string{
		"CURRENT STATE: BASELINE_COLLECTION",
		"TASK: Extract user concept into a profile.",
		"ALLOWED FIELDS: status, draft_profile(character_concept, name, age, tech_level, cp_limit).",
		"Your goal is to collect character profile data from the user",
		"Character Concept (e.g., Marine Mechanic, Noir Detective)",
		"Ask the user for missing details, or ask if they want them randomized/left blank.",
		"Do NOT generate the character sheet yet.",
		"Use machine-readable snake_case keys only inside draft_profile.",
		`{"status":"incomplete","draft_profile":{...}}`,
		`{"status":"complete","draft_profile":{...}}`,
		"Character Concept: Noir Detective",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Fatalf("expected gathering prompt to contain %q, got %q", check, prompt)
		}
	}
}

func TestAILocalStatePromptDictionaryContainsExpectedStates(t *testing.T) {
	checks := map[string]string{
		aiLocalPromptStateBaseline:     "CURRENT STATE: BASELINE_COLLECTION",
		aiLocalPromptStateBlueprint:    "CURRENT STATE: STEP_1_BLUEPRINT",
		aiLocalPromptStateStory:        "CURRENT STATE: STEP_2_STORY_ENGINE",
		aiLocalPromptStateAttributes:   "CURRENT STATE: STEP_3_ATTRIBUTES",
		aiLocalPromptStateAdvantages:   "CURRENT STATE: STEP_4_ADVANTAGES",
		aiLocalPromptStateSkills:       "CURRENT STATE: STEP_5_SKILLS",
		aiLocalPromptStateEquipment:    "CURRENT STATE: STEP_6_EQUIPMENT",
		aiLocalPromptStateContinuation: "CURRENT STATE: CONTINUATION_UPDATE",
	}
	for state, expected := range checks {
		prompt := aiLocalStatePrompt(state)
		if !strings.Contains(prompt, expected) {
			t.Fatalf("expected state prompt %q to contain %q, got %q", state, expected, prompt)
		}
	}
}

func TestAILocalBaselineDraftProfileJSONSchemaRequiresDraftProfile(t *testing.T) {
	schema, ok := aiLocalBaselineDraftProfileJSONSchema().(map[string]any)
	if !ok {
		t.Fatal("expected baseline schema to be a map")
	}
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("expected required field list, got %#v", schema["required"])
	}
	joined := strings.Join(required, ",")
	if !strings.Contains(joined, "status") || !strings.Contains(joined, "draft_profile") {
		t.Fatalf("expected baseline schema to require status and draft_profile, got %#v", required)
	}
}

func TestAIIsExplicitApprovalAcceptsCommonConfirmationPhrases(t *testing.T) {
	approved := []string{
		"Approve",
		"approved",
		"yes",
		"okay",
		"go ahead",
		"looks good",
		"create it",
		"build the character",
		"generate the sheet",
	}
	for _, input := range approved {
		if !aiIsExplicitApproval(input) {
			t.Fatalf("expected %q to count as approval", input)
		}
	}

	notApproved := []string{
		"change the hair color",
		"not yet",
		"do not approve",
		"add one more detail",
	}
	for _, input := range notApproved {
		if aiIsExplicitApproval(input) {
			t.Fatalf("expected %q to not count as approval", input)
		}
	}
}

func TestAIBuildBaselineApprovalMessageClarifiesSheetCreationState(t *testing.T) {
	message := aiBuildBaselineApprovalMessage(aiDraftProfile{
		CharacterConcept: aiFlexibleString("Noir Detective"),
		TechLevel:        aiFlexibleString("8"),
		CPLimit:          aiFlexibleString("150"),
	})
	checks := []string{
		"No character sheet has been created yet.",
		"Type \"Approve\", \"yes\", or \"go ahead\" to begin generation",
		"Character Concept: Noir Detective",
	}
	for _, check := range checks {
		if !strings.Contains(message, check) {
			t.Fatalf("expected approval message to contain %q, got %q", check, message)
		}
	}
}

func TestAIBuildBaselineEditModeMessageClarifiesNoSheetYet(t *testing.T) {
	message := aiBuildBaselineEditModeMessage()
	checks := []string{
		"generation has not started",
		"no character sheet has been created",
		"baseline-edit mode",
		"Reply with \"Approve\", \"yes\", or \"go ahead\" to start generation.",
	}
	for _, check := range checks {
		if !strings.Contains(message, check) {
			t.Fatalf("expected baseline edit mode message to contain %q, got %q", check, message)
		}
	}
}

func TestAIDraftProfileToCharacterRequestParams(t *testing.T) {
	defaults := aiCharacterRequestParams{TotalCP: 125, TechLevel: "3", Concept: "Fallback", StartingWealth: 1000, DisadvantageLimit: 30}
	got := aiDraftProfileToCharacterRequestParams(aiDraftProfile{
		CharacterConcept: aiFlexibleString("Marine Mechanic"),
		TechLevel:        aiFlexibleString("TL8"),
		CPLimit:          aiFlexibleString("200"),
		StartingWealth:   aiFlexibleString("$20,000"),
	}, defaults)
	if got.Concept != "Marine Mechanic" {
		t.Fatalf("expected concept %q, got %q", "Marine Mechanic", got.Concept)
	}
	if got.TechLevel != "8" {
		t.Fatalf("expected tech level 8, got %q", got.TechLevel)
	}
	if got.TotalCP != 200 {
		t.Fatalf("expected total CP 200, got %d", got.TotalCP)
	}
	if got.StartingWealth != 20000 {
		t.Fatalf("expected starting wealth 20000, got %d", got.StartingWealth)
	}
	if got.DisadvantageLimit != 30 {
		t.Fatalf("expected explicit disadvantage limit 30 to be preserved, got %d", got.DisadvantageLimit)
	}
}

func TestAIDraftProfileToCharacterRequestParamsRecomputesDerivedDisadvantageLimit(t *testing.T) {
	defaults := aiCharacterRequestParams{TotalCP: 75, TechLevel: "3", Concept: "Fallback", StartingWealth: 1000, DisadvantageLimit: aiDefaultDisadvantageLimit(75)}
	got := aiDraftProfileToCharacterRequestParams(aiDraftProfile{
		CharacterConcept: aiFlexibleString("Marine Mechanic"),
		TechLevel:        aiFlexibleString("TL8"),
		CPLimit:          aiFlexibleString("200"),
	}, defaults)
	if got.DisadvantageLimit != aiDefaultDisadvantageLimit(200) {
		t.Fatalf("expected derived disadvantage limit %d, got %d", aiDefaultDisadvantageLimit(200), got.DisadvantageLimit)
	}
	if got.StartingWealth != 20000 {
		t.Fatalf("expected default TL8 starting wealth 20000, got %d", got.StartingWealth)
	}
}

func TestAIParseLoosePositiveIntHandlesCurrencySeparators(t *testing.T) {
	if got := aiParseLoosePositiveInt("$20,000"); got != 20000 {
		t.Fatalf("expected 20000, got %d", got)
	}
}

func TestAIPrepareAIRequestStartsBaselineGathering(t *testing.T) {
	var d aiChatDockable
	prepared := d.prepareAIRequest("Build a 250-point TL8 cyberpunk investigator.")
	if d.buildSession == nil {
		t.Fatal("expected build session to be created")
	}
	if d.buildSession.State != aiBuildSessionStateGathering {
		t.Fatalf("expected state %q, got %q", aiBuildSessionStateGathering, d.buildSession.State)
	}
	if d.buildSession.DraftProfile.CharacterConcept.String() != "cyberpunk investigator" {
		t.Fatalf("expected concept %q, got %q", "cyberpunk investigator", d.buildSession.DraftProfile.CharacterConcept.String())
	}
	if d.buildSession.DraftProfile.TechLevel.String() != "8" {
		t.Fatalf("expected tech level 8, got %q", d.buildSession.DraftProfile.TechLevel.String())
	}
	if d.buildSession.DraftProfile.CPLimit.String() != "250" {
		t.Fatalf("expected cp limit 250, got %q", d.buildSession.DraftProfile.CPLimit.String())
	}
	if prepared.IsInitialBuild {
		t.Fatal("expected initial build request to stay in baseline gathering")
	}
	if !strings.Contains(prepared.SystemPrompt, "collect character profile data") {
		t.Fatalf("expected gathering prompt, got %q", prepared.SystemPrompt)
	}
}

func TestAIPrepareAIRequestUsesBuildContinuationSession(t *testing.T) {
	var d aiChatDockable
	d.buildSession = &aiBuildSessionContext{State: aiBuildSessionStateGenerating, Params: aiCharacterRequestParams{TotalCP: 150, TechLevel: "8", Concept: "mechanic", DisadvantageLimit: 50}}
	prepared := d.prepareAIRequest("add advantages")
	if !strings.Contains(prepared.SystemPrompt, "CURRENT STATE: CONTINUATION_UPDATE") {
		t.Fatalf("expected continuation state contract, got %q", prepared.SystemPrompt)
	}
	if !strings.Contains(prepared.SystemPrompt, "continuing an in-progress GURPS Fourth Edition character build") {
		t.Fatalf("expected continuation system prompt, got %q", prepared.SystemPrompt)
	}
	if !strings.Contains(prepared.SystemPrompt, "Return only changed fields for this turn") {
		t.Fatalf("expected continuation prompt to preserve incremental output contract, got %q", prepared.SystemPrompt)
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
