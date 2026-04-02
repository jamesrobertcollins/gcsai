package ux

import (
	"strings"
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

func TestAIPhase1OnlyActionPlanPreservesProfile(t *testing.T) {
	plan := aiPhase1OnlyActionPlan(aiActionPlan{
		Profile: &aiProfileAction{
			Name:   aiFlexibleString("Jozalyn Trenhalie"),
			Age:    aiFlexibleString("20"),
			Height: aiFlexibleString("5'8\""),
		},
		Attributes: []aiAttributeAction{{ID: aiFlexibleString("ST"), Value: aiFlexibleString("12")}},
		Skills:     []aiSkillAction{{Name: aiFlexibleString("Observation"), Points: aiFlexibleString("2")}},
		Equipment:  []aiNamedAction{{Name: aiFlexibleString("Backpack"), Quantity: aiFlexibleInt(1)}},
	})
	if plan.Profile == nil {
		t.Fatal("expected phase-1 filter to preserve profile")
	}
	if got := plan.Profile.Name.String(); got != "Jozalyn Trenhalie" {
		t.Fatalf("expected profile name to survive phase-1 filter, got %q", got)
	}
	if got := plan.Profile.Height.String(); got != "5'8\"" {
		t.Fatalf("expected profile height to survive phase-1 filter, got %q", got)
	}
	if len(plan.Skills) != 0 {
		t.Fatalf("expected phase-1 filter to drop skills, got %d", len(plan.Skills))
	}
	if len(plan.Equipment) != 0 {
		t.Fatalf("expected phase-1 filter to drop equipment, got %d", len(plan.Equipment))
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

func TestExecuteLocalCorrectionLoopResolvesFollowUpAlternatives(t *testing.T) {
	previousQuery := aiLocalCorrectionQueryModel
	previousResolveFiltered := aiLocalCorrectionResolveFiltered
	previousResolvePlan := aiLocalCorrectionResolvePlan
	t.Cleanup(func() {
		aiLocalCorrectionQueryModel = previousQuery
		aiLocalCorrectionResolveFiltered = previousResolveFiltered
		aiLocalCorrectionResolvePlan = previousResolvePlan
	})

	queryCalls := 0
	aiLocalCorrectionQueryModel = func(d *aiChatDockable, endpoint, model string, messages []aiLocalChatMessage, schema any) (string, error) {
		queryCalls++
		if len(messages) != 2 || !strings.Contains(messages[1].Content, "Soulbound Hammer") {
			t.Fatalf("expected correction prompt to reference unresolved item, got %#v", messages)
		}
		return `{}`, nil
	}
	aiLocalCorrectionResolveFiltered = func(d *aiChatDockable, responseText string, filter func(aiActionPlan) aiActionPlan) (aiPlanResolutionResult, error) {
		plan := filter(aiActionPlan{
			Advantages: []aiNamedAction{{Name: aiFlexibleString("Signature Gear"), Description: aiFlexibleString("Sentient relic"), Points: aiFlexibleString("5")}},
		})
		return aiPlanResolutionResult{Parsed: true, Plan: plan, ResolvedPlan: plan}, nil
	}
	aiLocalCorrectionResolvePlan = func(d *aiChatDockable, plan aiActionPlan) (aiPlanResolutionResult, error) {
		return aiPlanResolutionResult{Parsed: true, Plan: plan, ResolvedPlan: plan}, nil
	}

	resolution := aiPlanResolutionResult{
		Parsed: true,
		Plan: aiActionPlan{
			Advantages: []aiNamedAction{{Name: aiFlexibleString("Soulbound Hammer"), Description: aiFlexibleString("Sentient relic"), Points: aiFlexibleString("5")}},
		},
		RetryItems: []aiRetryItem{{Category: string(aiLibraryCategoryAdvantage), Name: "Soulbound Hammer", Description: "Sentient relic", Points: "5"}},
	}

	var dockable aiChatDockable
	resolved, err := dockable.executeLocalCorrectionLoop("http://local", "test-model", "system", "Phase 1", resolution, aiPhase1OnlyActionPlan)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if queryCalls != 1 {
		t.Fatalf("expected 1 correction query, got %d", queryCalls)
	}
	if len(resolved.RetryItems) != 0 {
		t.Fatalf("expected retry items to be cleared, got %#v", resolved.RetryItems)
	}
	if len(resolved.ResolvedPlan.Advantages) != 1 || resolved.ResolvedPlan.Advantages[0].Name.String() != "Signature Gear" {
		t.Fatalf("expected Signature Gear to remain after correction, got %#v", resolved.ResolvedPlan.Advantages)
	}
	if len(resolved.Warnings) == 0 || !strings.Contains(resolved.Warnings[0], "reduced unresolved items from 1 to 0") {
		t.Fatalf("expected progress warning, got %#v", resolved.Warnings)
	}
}

func TestExecuteLocalCorrectionLoopHardStopsOnPointBearingItems(t *testing.T) {
	previousQuery := aiLocalCorrectionQueryModel
	previousResolveFiltered := aiLocalCorrectionResolveFiltered
	previousResolvePlan := aiLocalCorrectionResolvePlan
	t.Cleanup(func() {
		aiLocalCorrectionQueryModel = previousQuery
		aiLocalCorrectionResolveFiltered = previousResolveFiltered
		aiLocalCorrectionResolvePlan = previousResolvePlan
	})

	aiLocalCorrectionQueryModel = func(d *aiChatDockable, endpoint, model string, messages []aiLocalChatMessage, schema any) (string, error) {
		return `{}`, nil
	}
	aiLocalCorrectionResolveFiltered = func(d *aiChatDockable, responseText string, filter func(aiActionPlan) aiActionPlan) (aiPlanResolutionResult, error) {
		plan := filter(aiActionPlan{
			Advantages: []aiNamedAction{{Name: aiFlexibleString("Soulbound Hammer"), Description: aiFlexibleString("Sentient relic"), Points: aiFlexibleString("5")}},
		})
		return aiPlanResolutionResult{Parsed: true, Plan: plan}, nil
	}
	aiLocalCorrectionResolvePlan = func(d *aiChatDockable, plan aiActionPlan) (aiPlanResolutionResult, error) {
		return aiPlanResolutionResult{
			Parsed:     true,
			Plan:       plan,
			RetryItems: []aiRetryItem{{Category: string(aiLibraryCategoryAdvantage), Name: "Soulbound Hammer", Description: "Sentient relic", Points: "5"}},
		}, nil
	}

	resolution := aiPlanResolutionResult{
		Parsed: true,
		Plan: aiActionPlan{
			Advantages: []aiNamedAction{{Name: aiFlexibleString("Soulbound Hammer"), Description: aiFlexibleString("Sentient relic"), Points: aiFlexibleString("5")}},
		},
		RetryItems: []aiRetryItem{{Category: string(aiLibraryCategoryAdvantage), Name: "Soulbound Hammer", Description: "Sentient relic", Points: "5"}},
	}

	var dockable aiChatDockable
	resolved, err := dockable.executeLocalCorrectionLoop("http://local", "test-model", "system", "Phase 1", resolution, aiPhase1OnlyActionPlan)
	if err == nil {
		t.Fatal("expected hard-stop error for unresolved point-bearing item")
	}
	if !strings.Contains(err.Error(), "still has unresolved point-bearing items") {
		t.Fatalf("expected hard-stop error text, got %v", err)
	}
	if len(resolved.Warnings) == 0 || !strings.Contains(resolved.Warnings[0], "made no progress and was stopped") {
		t.Fatalf("expected no-progress warning, got %#v", resolved.Warnings)
	}
}

func TestExecuteLocalCorrectionLoopAllowsEquipmentRetriesToRemain(t *testing.T) {
	previousQuery := aiLocalCorrectionQueryModel
	previousResolveFiltered := aiLocalCorrectionResolveFiltered
	previousResolvePlan := aiLocalCorrectionResolvePlan
	t.Cleanup(func() {
		aiLocalCorrectionQueryModel = previousQuery
		aiLocalCorrectionResolveFiltered = previousResolveFiltered
		aiLocalCorrectionResolvePlan = previousResolvePlan
	})

	aiLocalCorrectionQueryModel = func(d *aiChatDockable, endpoint, model string, messages []aiLocalChatMessage, schema any) (string, error) {
		return `{}`, nil
	}
	aiLocalCorrectionResolveFiltered = func(d *aiChatDockable, responseText string, filter func(aiActionPlan) aiActionPlan) (aiPlanResolutionResult, error) {
		plan := filter(aiActionPlan{
			Equipment: []aiNamedAction{{Name: aiFlexibleString("Mystery Relic"), Description: aiFlexibleString("Indestructible keepsake"), Quantity: aiFlexibleInt(1)}},
		})
		return aiPlanResolutionResult{Parsed: true, Plan: plan}, nil
	}
	aiLocalCorrectionResolvePlan = func(d *aiChatDockable, plan aiActionPlan) (aiPlanResolutionResult, error) {
		return aiPlanResolutionResult{
			Parsed:     true,
			Plan:       plan,
			RetryItems: []aiRetryItem{{Category: string(aiLibraryCategoryEquipment), Name: "Mystery Relic", Description: "Indestructible keepsake", Quantity: 1}},
		}, nil
	}

	resolution := aiPlanResolutionResult{
		Parsed: true,
		Plan: aiActionPlan{
			Equipment: []aiNamedAction{{Name: aiFlexibleString("Mystery Relic"), Description: aiFlexibleString("Indestructible keepsake"), Quantity: aiFlexibleInt(1)}},
		},
		RetryItems: []aiRetryItem{{Category: string(aiLibraryCategoryEquipment), Name: "Mystery Relic", Description: "Indestructible keepsake", Quantity: 1}},
	}

	var dockable aiChatDockable
	resolved, err := dockable.executeLocalCorrectionLoop("http://local", "test-model", "system", "Phase 2", resolution, aiPhase2OnlyActionPlan)
	if err != nil {
		t.Fatalf("expected equipment-only unresolved items to be allowed, got %v", err)
	}
	if len(resolved.RetryItems) != 1 {
		t.Fatalf("expected equipment retry item to remain, got %#v", resolved.RetryItems)
	}
	if len(resolved.Warnings) == 0 || !strings.Contains(resolved.Warnings[0], "made no progress and was stopped") {
		t.Fatalf("expected no-progress warning, got %#v", resolved.Warnings)
	}
}

func TestExecuteLocalCorrectionLoopIgnoresUnrelatedCorrectionEntries(t *testing.T) {
	previousQuery := aiLocalCorrectionQueryModel
	previousResolveFiltered := aiLocalCorrectionResolveFiltered
	previousResolvePlan := aiLocalCorrectionResolvePlan
	t.Cleanup(func() {
		aiLocalCorrectionQueryModel = previousQuery
		aiLocalCorrectionResolveFiltered = previousResolveFiltered
		aiLocalCorrectionResolvePlan = previousResolvePlan
	})

	aiLocalCorrectionQueryModel = func(d *aiChatDockable, endpoint, model string, messages []aiLocalChatMessage, schema any) (string, error) {
		return `{}`, nil
	}
	aiLocalCorrectionResolveFiltered = func(d *aiChatDockable, responseText string, filter func(aiActionPlan) aiActionPlan) (aiPlanResolutionResult, error) {
		plan := filter(aiActionPlan{
			Advantages: []aiNamedAction{{Name: aiFlexibleString("Heroic Feat of Strength"), Points: aiFlexibleString("-5")}},
			Disadvantages: []aiNamedAction{
				{Name: aiFlexibleString("Demonic Attunement"), Points: aiFlexibleString("-5")},
				{Name: aiFlexibleString("Clerical Investment"), Points: aiFlexibleString("-10")},
			},
		})
		return aiPlanResolutionResult{Parsed: true, Plan: plan}, nil
	}
	aiLocalCorrectionResolvePlan = func(d *aiChatDockable, plan aiActionPlan) (aiPlanResolutionResult, error) {
		return aiPlanResolutionResult{
			Parsed:     true,
			Plan:       plan,
			RetryItems: []aiRetryItem{{Category: string(aiLibraryCategoryAdvantage), Name: "Heroic Feat of Strength", Points: "-5"}},
		}, nil
	}

	resolution := aiPlanResolutionResult{
		Parsed: true,
		Plan: aiActionPlan{
			Advantages: []aiNamedAction{{Name: aiFlexibleString("Heroic Feat of Strength"), Points: aiFlexibleString("-5")}},
		},
		RetryItems: []aiRetryItem{{Category: string(aiLibraryCategoryAdvantage), Name: "Heroic Feat of Strength", Points: "-5", Candidates: []aiRetryCandidate{{ID: "trait-heroic", Name: "Heroic Feats of @One of Strength, Dexterity, or Health@"}}}},
	}

	var dockable aiChatDockable
	resolved, err := dockable.executeLocalCorrectionLoop("http://local", "test-model", "system", "Phase 1", resolution, aiPhase1OnlyActionPlan)
	if err == nil {
		t.Fatal("expected hard-stop error when unrelated correction entries are ignored and the original retry item remains unresolved")
	}
	if len(resolved.RetryItems) != 1 {
		t.Fatalf("expected only the original retry item to remain, got %#v", resolved.RetryItems)
	}
	if strings.Contains(aiRetryItemsSummary(resolved.RetryItems), "Demonic Attunement") || strings.Contains(aiRetryItemsSummary(resolved.RetryItems), "Clerical Investment") {
		t.Fatalf("expected unrelated correction entries to be excluded, got %#v", resolved.RetryItems)
	}
	joinedWarnings := strings.Join(resolved.Warnings, "\n")
	if !strings.Contains(joinedWarnings, "ignored 3 unrelated correction entries") {
		t.Fatalf("expected unrelated correction warning, got %#v", resolved.Warnings)
	}
	if !strings.Contains(joinedWarnings, "made no progress and was stopped") {
		t.Fatalf("expected no-progress warning after filtering unrelated entries, got %#v", resolved.Warnings)
	}
}
