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
