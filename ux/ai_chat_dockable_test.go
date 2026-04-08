package ux

import (
	"strings"
	"testing"

	"github.com/google/generative-ai-go/genai"
	"github.com/richardwilkes/gcs/v5/model/fxp"
	"github.com/richardwilkes/gcs/v5/model/gurps"
)

func TestAddOrUpdateSkillUsesPendingSkillList(t *testing.T) {
	entity := gurps.NewEntity()
	existing := gurps.NewSkill(entity, nil, false)
	existing.Name = "Mechanic"
	existing.Specialization = "Automobile"
	existing.Points = fxp.One
	workingSkills := []*gurps.Skill{existing}

	var dockable aiChatDockable
	updatedSkills, warning, retryItem, err := dockable.addOrUpdateSkill(entity, workingSkills, aiSkillAction{
		Name:        aiFlexibleString("Mechanic (Automobile)"),
		Description: aiFlexibleString("Keeps engines running with improvised spare parts."),
		Points:      aiFlexibleString("4"),
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	if retryItem != nil {
		t.Fatalf("expected no retry item, got %#v", retryItem)
	}
	if len(updatedSkills) != 1 {
		t.Fatalf("expected a single working skill, got %d", len(updatedSkills))
	}
	if updatedSkills[0] != existing {
		t.Fatal("expected existing working skill to be reused")
	}
	if existing.Points != fxp.FromInteger(4) {
		t.Fatalf("expected existing skill points to update to 4, got %v", existing.Points)
	}
	if existing.LocalNotes != "Keeps engines running with improvised spare parts." {
		t.Fatalf("expected existing skill description to persist in local notes, got %q", existing.LocalNotes)
	}
	if len(entity.Skills) != 0 {
		t.Fatalf("expected entity skills to remain uncommitted during update, got %d", len(entity.Skills))
	}
}

func TestAIHistoryWithLastAssistantSummaryReplacesRawResponse(t *testing.T) {
	history := []*genai.Content{
		{Role: "user", Parts: []genai.Part{genai.Text("Build a 150-point knight")}},
		{Role: "model", Parts: []genai.Part{genai.Text(`{"profile":{"name":"Thomas Smith"},"skills":[{"name":"Broadsword","points":"4"}]}`)}},
	}
	plan := aiActionPlan{
		Profile: &aiProfileAction{Name: aiFlexibleString("Thomas Smith")},
		Skills:  []aiSkillAction{{Name: aiFlexibleString("Broadsword"), Points: aiFlexibleString("4")}},
	}

	updated := aiHistoryWithLastAssistantSummary(history, plan)
	if len(updated) != len(history) {
		t.Fatalf("expected history length %d, got %d", len(history), len(updated))
	}
	if updated[1] == history[1] {
		t.Fatal("expected assistant entry to be replaced with a compact summary")
	}
	text := aiMarshalLocalContent(updated[1])
	if strings.Contains(text, `"skills"`) || strings.Contains(text, `"profile"`) {
		t.Fatalf("expected raw JSON to be removed from history, got %q", text)
	}
	if !strings.Contains(text, "Applied character-sheet update.") {
		t.Fatalf("expected summary marker in history, got %q", text)
	}
	if !strings.Contains(text, "profile") || !strings.Contains(text, "1 skills") {
		t.Fatalf("expected updated categories in summary, got %q", text)
	}
}

func TestAIParseLocalBaselineDraftProfileResponseAcceptsTitleCaseKeys(t *testing.T) {
	responseText := `Here is the updated draft profile:
{"status":"complete","draft_profile":{"Character Concept":"desert dweller","Name":"randomized","Title":"meth-head","TL":8,"CP Limit":100,"Starting Wealth":"$10,000","Game Setting":""}}`
	response, ok := aiParseLocalBaselineDraftProfileResponse(responseText)
	if !ok {
		t.Fatal("expected baseline parser to accept title-cased draft_profile keys")
	}
	if response.DraftProfile.CharacterConcept.String() != "desert dweller" {
		t.Fatalf("expected concept to be parsed, got %q", response.DraftProfile.CharacterConcept.String())
	}
	if response.DraftProfile.TechLevel.String() != "8" {
		t.Fatalf("expected tech level 8, got %q", response.DraftProfile.TechLevel.String())
	}
	if response.DraftProfile.CPLimit.String() != "100" {
		t.Fatalf("expected cp limit 100, got %q", response.DraftProfile.CPLimit.String())
	}
	if response.DraftProfile.StartingWealth.String() != "$10,000" {
		t.Fatalf("expected starting wealth to be parsed, got %q", response.DraftProfile.StartingWealth.String())
	}
}

func TestAIValidateLocalBaselineCollectionResponseTextRejectsCrossSystemDraftProfile(t *testing.T) {
	responseText := `{"status":"incomplete","draft_profile":{"name":"Wong Jick","class":"Assassin","race":"Human","ac":"10","hp":"12","saving_throws":["Dex"],"features":["Sneak Attack"],"skills":[]}}`
	err := aiValidateLocalBaselineCollectionResponseText(responseText)
	if err == nil {
		t.Fatal("expected cross-system baseline payload to be rejected")
	}
	message := err.Error()
	checks := []string{"cross-system keys", "class", "ac", "saving_throws", "non-baseline keys", "skills"}
	for _, check := range checks {
		if !strings.Contains(message, check) {
			t.Fatalf("expected validation error to mention %q, got %q", check, message)
		}
	}
}

func TestAIValidateLocalBaselineCollectionResponseTextRejectsCharacterSheetPayload(t *testing.T) {
	responseText := `{"status":"complete","character_sheet":{"name":"Wong Jick","attributes":{"ST":11}}}`
	err := aiValidateLocalBaselineCollectionResponseText(responseText)
	if err == nil {
		t.Fatal("expected full character-sheet payload to be rejected during baseline collection")
	}
	if !strings.Contains(err.Error(), "full character-sheet payload") {
		t.Fatalf("expected full character-sheet validation error, got %q", err.Error())
	}
}

func TestParseAIActionPlanAcceptsWrappedCharacterSheet(t *testing.T) {
	var d aiChatDockable
	responseText := `{"status":"complete","character_sheet":{"Name":"Bubba Jenkins","Title":"Meth-Head","TL":8,"Attributes":{"ST":11,"DX":12},"Advantages":["Temperature Tolerance"],"Disadvantages":["Bad Reputation"],"Skills":{"Driving (Automobile)":12},"Equipment":["Knife"]}}`
	plan, ok := d.parseAIActionPlan(responseText)
	if !ok {
		t.Fatal("expected wrapped character_sheet response to be converted into an action plan")
	}
	if plan.Profile == nil || plan.Profile.Name.String() != "Bubba Jenkins" {
		t.Fatalf("expected wrapped profile to populate action plan profile, got %#v", plan.Profile)
	}
	if len(plan.Attributes) != 2 {
		t.Fatalf("expected wrapped attributes to populate action plan, got %d", len(plan.Attributes))
	}
	if len(plan.Skills) != 1 || plan.Skills[0].Name.String() != "Driving (Automobile)" {
		t.Fatalf("expected wrapped skills to populate action plan, got %#v", plan.Skills)
	}
	if len(plan.Equipment) != 1 || plan.Equipment[0].Name.String() != "Knife" {
		t.Fatalf("expected wrapped equipment to populate action plan, got %#v", plan.Equipment)
	}
}

func TestAIFlexibleStringStringNormalizesInvalidUTF8(t *testing.T) {
	value := aiFlexibleString(string([]byte{'A', 0xff, 'B'}))
	if got := value.String(); got != "A\ufffdB" {
		t.Fatalf("expected flexible string to normalize invalid UTF-8, got %q", got)
	}
}

func TestApplyProfileActionNormalizesInvalidUTF8(t *testing.T) {
	entity := gurps.NewEntity()
	applyProfileAction(entity, &aiProfileAction{
		Name:  aiFlexibleString(string([]byte{'A', 0xff, 'B'})),
		Title: aiFlexibleString(string([]byte{'X', 0xff, 'Y'})),
	})
	if entity.Profile.Name != "A\ufffdB" {
		t.Fatalf("expected profile name to be normalized, got %q", entity.Profile.Name)
	}
	if entity.Profile.Title != "X\ufffdY" {
		t.Fatalf("expected profile title to be normalized, got %q", entity.Profile.Title)
	}
}
