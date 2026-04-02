package ux

import (
	"strings"
	"testing"
)

func TestAIBuildLocalResolverAlternativePrompt(t *testing.T) {
	items := []aiRetryItem{
		{
			Category:    string(aiLibraryCategorySkill),
			Name:        "Handguns",
			Notes:       "Urban",
			Description: "Streetwise sidearm practice",
			Points:      "2",
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
		"The following GURPS 4e items could not be resolved to exact library entries:",
		"- skill: \"Handguns\" | notes=\"Urban\" | description=\"Streetwise sidearm practice\" | points=\"2\"",
		"candidate id=skill-pistol | name=Guns (Pistol)",
		"candidate id=skill-smg | name=Guns (SMG)",
		"- equipment: \"Healing Potion\" | quantity=3",
		"When candidates are listed, use the exact candidate id and candidate name shown.",
		"preserve points, quantity, notes, and description",
		"Use valid GURPS 4e names",
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
			{Name: aiFlexibleString("Rope"), Description: aiFlexibleString("climbing rope"), Quantity: aiFlexibleInt(2)},
			{Name: aiFlexibleString("Rope"), Description: aiFlexibleString("camp rope"), Quantity: aiFlexibleInt(2)},
			{Name: aiFlexibleString("Lantern"), Quantity: aiFlexibleInt(1)},
		},
	}

	filtered := aiActionPlanWithoutRetryItems(plan, []aiRetryItem{
		{Category: string(aiLibraryCategoryAdvantage), Name: "Combat Reflexes", Points: "15"},
		{Category: string(aiLibraryCategorySkill), Name: "Area Knowledge (Mesa)", Notes: "Mesa", Points: "12"},
		{Category: string(aiLibraryCategoryEquipment), Name: "Rope", Description: "climbing rope", Quantity: 2},
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
	if len(filtered.Equipment) != 2 {
		t.Fatalf("expected Lantern and one Rope to remain in equipment, got %#v", filtered.Equipment)
	}
	if filtered.Equipment[0].Description.String() != "camp rope" || filtered.Equipment[1].Name.String() != "Lantern" {
		t.Fatalf("expected camp rope and Lantern to remain in equipment, got %#v", filtered.Equipment)
	}
}

func TestAIFilterCorrectionPlanDropsUnrelatedEntries(t *testing.T) {
	plan := aiActionPlan{
		Advantages: []aiNamedAction{{ID: aiFlexibleString("trait-heroic"), Name: aiFlexibleString("Heroic Feats of @One of Strength, Dexterity, or Health@")}},
		Disadvantages: []aiNamedAction{
			{Name: aiFlexibleString("Demonic Attunement")},
			{Name: aiFlexibleString("Clerical Investment")},
		},
	}

	filtered := aiFilterCorrectionPlan(plan, []aiRetryItem{{
		Category: string(aiLibraryCategoryAdvantage),
		Name:     "Heroic Feat of Strength",
		Points:   "-5",
		Candidates: []aiRetryCandidate{
			{ID: "trait-heroic", Name: "Heroic Feats of @One of Strength, Dexterity, or Health@"},
		},
	}})

	if len(filtered.Advantages) != 1 {
		t.Fatalf("expected matching correction advantage to remain, got %#v", filtered.Advantages)
	}
	if len(filtered.Disadvantages) != 0 {
		t.Fatalf("expected unrelated disadvantages to be dropped, got %#v", filtered.Disadvantages)
	}
}
