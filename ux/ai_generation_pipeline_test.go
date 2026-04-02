package ux

import (
	"testing"

	"github.com/richardwilkes/gcs/v5/model/fxp"
	"github.com/richardwilkes/gcs/v5/model/gurps"
)

func TestSnapToValidSkillPoints(t *testing.T) {
	tests := map[int]int{
		-1: 1,
		0:  1,
		1:  1,
		2:  2,
		3:  2,
		4:  4,
		5:  4,
		6:  4,
		7:  8,
		10: 8,
		11: 12,
		18: 16,
		19: 20,
	}
	for requested, expected := range tests {
		if got := SnapToValidSkillPoints(requested); got != expected {
			t.Fatalf("expected %d to snap to %d, got %d", requested, expected, got)
		}
	}
}

func TestAIPhase2OnlyActionPlanSnapsSkillPoints(t *testing.T) {
	plan := aiSnapSkillPointsInPlan(aiPhase2OnlyActionPlan(aiActionPlan{
		Attributes: []aiAttributeAction{{ID: aiFlexibleString("ST"), Value: aiFlexibleString("12")}},
		Skills: []aiSkillAction{
			{Name: aiFlexibleString("Brawling"), Points: aiFlexibleString("3")},
			{Name: aiFlexibleString("Stealth"), Value: aiFlexibleString("10")},
		},
		Equipment: []aiNamedAction{{Name: aiFlexibleString("Tool Kit"), Quantity: aiFlexibleInt(1)}},
	}))
	if len(plan.Attributes) != 0 {
		t.Fatalf("expected phase-2 filter to drop attributes, got %d", len(plan.Attributes))
	}
	if len(plan.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(plan.Skills))
	}
	if got := plan.Skills[0].Points.String(); got != "2" {
		t.Fatalf("expected first skill to snap to 2 points, got %q", got)
	}
	if got := plan.Skills[1].Points.String(); got != "8" {
		t.Fatalf("expected second skill to snap to 8 points, got %q", got)
	}
	if got := plan.Skills[1].Value.String(); got != "" {
		t.Fatalf("expected snapped skill value field to be cleared, got %q", got)
	}
}

func TestAutoBalanceUnspentPointsUsesExistingSkillSteps(t *testing.T) {
	entity := gurps.NewEntity()
	entity.TotalPoints = fxp.FromInteger(4)
	skill := gurps.NewSkill(entity, nil, false)
	skill.Name = "Brawling"
	skill.Points = fxp.One
	entity.SetSkillList([]*gurps.Skill{skill})
	entity.Recalculate()

	AutoBalanceUnspentPoints(entity, 4)

	if got := fxp.AsInteger[int](entity.UnspentPoints()); got != 0 {
		t.Fatalf("expected zero unspent points, got %d", got)
	}
	if got := fxp.AsInteger[int](skill.Points); got != 4 {
		t.Fatalf("expected skill points to end at 4, got %d", got)
	}
	if got := fxp.AsInteger[int](entity.TotalPoints); got != 4 {
		t.Fatalf("expected target total points to remain 4, got %d", got)
	}
}
