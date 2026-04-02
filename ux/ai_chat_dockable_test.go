package ux

import (
	"testing"

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
