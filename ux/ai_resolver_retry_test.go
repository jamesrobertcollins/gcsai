package ux

import (
	"strings"
	"testing"
)

func TestAIBuildLocalResolverAlternativePrompt(t *testing.T) {
	items := []aiRetryItem{
		{
			Category: string(aiLibraryCategorySkill),
			Name:     "Handguns",
			Notes:    "Urban",
			Points:   "2",
			Candidates: []aiRetryCandidate{
				{ID: "skill-pistol", Name: "Guns (Pistol)"},
				{ID: "skill-smg", Name: "Guns (SMG)"},
			},
		},
		{
			Category: string(aiLibraryCategoryEquipment),
			Name:     "Healing Potion",
			Quantity: 3,
		},
	}

	prompt := aiBuildLocalResolverAlternativePrompt(items)
	checks := []string{
		"The following traits do not exist in the GURPS 4e library:",
		"- skill: \"Handguns\" | notes=\"Urban\" | points=\"2\" | close matches: Guns (Pistol), Guns (SMG)",
		"- equipment: \"Healing Potion\" | quantity=3",
		"Please provide valid GURPS 4e alternatives for these.",
		"Return ONLY a single JSON object with replacement entries using the same category fields",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Fatalf("expected prompt to contain %q, got %q", check, prompt)
		}
	}
}

func TestAIActionPlanWithoutRetryItemsRemovesMatchingActions(t *testing.T) {
	plan := aiActionPlan{
		Advantages: []aiNamedAction{
			{Name: aiFlexibleString("Combat Reflexes"), Points: aiFlexibleString("15")},
			{Name: aiFlexibleString("Luck"), Points: aiFlexibleString("15")},
		},
		Disadvantages: []aiNamedAction{{Name: aiFlexibleString("Bad Temper"), Points: aiFlexibleString("-10")}},
		Skills: []aiSkillAction{
			{Name: aiFlexibleString("Area Knowledge (Mesa)"), Notes: aiFlexibleString("Mesa"), Value: aiFlexibleString("12")},
			{Name: aiFlexibleString("Stealth"), Points: aiFlexibleString("2")},
		},
		Equipment: []aiNamedAction{
			{Name: aiFlexibleString("Rope"), Quantity: aiFlexibleInt(2)},
			{Name: aiFlexibleString("Lantern"), Quantity: aiFlexibleInt(1)},
		},
	}

	filtered := aiActionPlanWithoutRetryItems(plan, []aiRetryItem{
		{Category: string(aiLibraryCategoryAdvantage), Name: "Combat Reflexes", Points: "15"},
		{Category: string(aiLibraryCategorySkill), Name: "Area Knowledge (Mesa)", Notes: "Mesa", Points: "12"},
		{Category: string(aiLibraryCategoryEquipment), Name: "Rope", Quantity: 2},
	})

	if len(filtered.Advantages) != 1 || filtered.Advantages[0].Name.String() != "Luck" {
		t.Fatalf("expected only Luck to remain in advantages, got %#v", filtered.Advantages)
	}
	if len(filtered.Disadvantages) != 1 || filtered.Disadvantages[0].Name.String() != "Bad Temper" {
		t.Fatalf("expected disadvantages to remain unchanged, got %#v", filtered.Disadvantages)
	}
	if len(filtered.Skills) != 1 || filtered.Skills[0].Name.String() != "Stealth" {
		t.Fatalf("expected only Stealth to remain in skills, got %#v", filtered.Skills)
	}
	if len(filtered.Equipment) != 1 || filtered.Equipment[0].Name.String() != "Lantern" {
		t.Fatalf("expected only Lantern to remain in equipment, got %#v", filtered.Equipment)
	}
}
