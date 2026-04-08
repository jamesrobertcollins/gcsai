package ux

import (
	"strings"
	"testing"

	"github.com/richardwilkes/gcs/v5/model/criteria"
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

func TestAISkillsAndSpellsOnlyActionPlanSnapsSkillPoints(t *testing.T) {
	plan := aiSkillsAndSpellsOnlyActionPlan(aiActionPlan{
		Attributes: []aiAttributeAction{{ID: aiFlexibleString("ST"), Value: aiFlexibleString("12")}},
		Skills: []aiSkillAction{
			{Name: aiFlexibleString("Brawling"), Points: aiFlexibleString("3")},
			{Name: aiFlexibleString("Stealth"), Value: aiFlexibleString("10")},
		},
		Spells:    []aiSkillAction{{Name: aiFlexibleString("Fireball"), Points: aiFlexibleString("3")}},
		Equipment: []aiNamedAction{{Name: aiFlexibleString("Tool Kit"), Quantity: aiFlexibleInt(1)}},
	})
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
	if len(plan.Spells) != 1 {
		t.Fatalf("expected 1 spell, got %d", len(plan.Spells))
	}
	if got := plan.Spells[0].Points.String(); got != "2" {
		t.Fatalf("expected spell points to snap to 2, got %q", got)
	}
}

func TestAIGenerationBudgetFromPercentagesAllocatesExactCP(t *testing.T) {
	budget := aiGenerationBudgetFromPercentages(150, aiBlueprintBudgetPercentages{
		Attributes:       aiFlexibleInt(40),
		Advantages:       aiFlexibleInt(20),
		CoreSkills:       aiFlexibleInt(25),
		BackgroundSkills: aiFlexibleInt(15),
	})
	if budget.TotalCP != 150 {
		t.Fatalf("expected total cp 150, got %d", budget.TotalCP)
	}
	if budget.Attributes != 60 || budget.Advantages != 30 || budget.CoreSkills != 38 || budget.BackgroundSkills != 22 {
		t.Fatalf("unexpected budget allocation: %#v", budget)
	}
	if got := budget.Attributes + budget.Advantages + budget.CoreSkills + budget.BackgroundSkills; got != 150 {
		t.Fatalf("expected exact CP allocation, got %d", got)
	}
}

func TestGenerationBudgetAddDisadvantageBonusSplitsAcrossAdvantagesAndSkills(t *testing.T) {
	budget := GenerationBudget{TotalCP: 150, Attributes: 60, Advantages: 30, CoreSkills: 38, BackgroundSkills: 22}
	budget.AddDisadvantageBonus(11)
	if budget.Advantages != 35 {
		t.Fatalf("expected advantages to gain 5 CP, got %d", budget.Advantages)
	}
	if budget.CoreSkills != 42 || budget.BackgroundSkills != 24 {
		t.Fatalf("expected skill bonus to preserve skill-bucket weighting, got core=%d background=%d", budget.CoreSkills, budget.BackgroundSkills)
	}
}

func TestAIParseGenerationBlueprintResponse(t *testing.T) {
	response := `{"themes":["Marine","Mechanic","Veteran"],"budget_percentages":{"attributes":40,"advantages":20,"core_skills":25,"background_skills":15}}`
	themes, budget, err := aiParseGenerationBlueprintResponse(response, 150, "marine mechanic")
	if err != nil {
		t.Fatalf("expected blueprint parse to succeed, got %v", err)
	}
	if strings.Join(themes, ",") != "Marine,Mechanic,Veteran" {
		t.Fatalf("unexpected themes: %#v", themes)
	}
	if budget.Attributes+budget.Advantages+budget.CoreSkills+budget.BackgroundSkills != 150 {
		t.Fatalf("expected budget to sum to 150, got %#v", budget)
	}
	if budget.Attributes != 60 || budget.Advantages != 30 || budget.CoreSkills != 38 || budget.BackgroundSkills != 22 {
		t.Fatalf("unexpected budget allocation: %#v", budget)
	}
}

func TestAIFilterThematicVocabularySections(t *testing.T) {
	vocabulary := "Thematic Canonical GURPS Vocabulary:\n- Skills: Mechanic (Automobile)\n- Spells: Mend\n- Advantages: Signature Gear\n- Perks: Craftiness\n- Disadvantages: Overconfidence\n- Quirks: Keeps tools immaculate"
	filtered := aiFilterThematicVocabularySections(vocabulary, "Disadvantages", "Quirks")
	if strings.Contains(filtered, "Skills") || strings.Contains(filtered, "Spells") || strings.Contains(filtered, "Advantages") || strings.Contains(filtered, "Perks") {
		t.Fatalf("expected filtered vocabulary to exclude non-story sections, got %q", filtered)
	}
	if !strings.Contains(filtered, "Disadvantages: Overconfidence") || !strings.Contains(filtered, "Quirks: Keeps tools immaculate") {
		t.Fatalf("expected filtered vocabulary to keep story sections, got %q", filtered)
	}
}

func TestAIBuildLocalEquipmentPrompts(t *testing.T) {
	systemPrompt, userPrompt := aiBuildLocalEquipmentPrompts(
		"Build a TL8 marine mechanic.",
		aiCharacterRequestParams{TotalCP: 150, TechLevel: "8", Concept: "marine mechanic", StartingWealth: 20000, DisadvantageLimit: 50},
		[]string{"Marine", "Mechanic", "Veteran"},
		20000,
		"Current character sheet context: skills and advantages already applied.",
		"Thematic Canonical GURPS Vocabulary:\n- Equipment: Tool Kit, Light Scale Armor",
	)
	combined := systemPrompt + "\n" + userPrompt
	checks := []string{
		"deterministic GURPS 4e equipment generator",
		"You may output ONLY the \"equipment\" field.",
		"You have exactly $20,000 to spend on mundane equipment, weapons, and armor.",
		"Do NOT attempt to generate custom Attack blocks or combat stats; simply purchase the weapons.",
		"Do not assign CP values to items unless it is explicitly customized Signature Gear.",
		"Tech Level: TL 8",
		"Current character sheet context: skills and advantages already applied.",
	}
	for _, check := range checks {
		if !strings.Contains(combined, check) {
			t.Fatalf("expected equipment prompts to contain %q, got %q", check, combined)
		}
	}
}

func TestAIAdjustedStartingWealthForEntity(t *testing.T) {
	entity := gurps.NewEntity()
	wealthy := gurps.NewTrait(entity, nil, false)
	wealthy.Name = "Wealthy"
	wealthy.BasePoints = fxp.FromInteger(20)
	entity.SetTraitList([]*gurps.Trait{wealthy})
	entity.Recalculate()

	adjusted, wealthTrait := aiAdjustedStartingWealthForEntity(entity, 20000)
	if adjusted != 100000 {
		t.Fatalf("expected Wealthy to raise starting wealth to 100000, got %d", adjusted)
	}
	if wealthTrait != "Wealthy" {
		t.Fatalf("expected wealth trait label %q, got %q", "Wealthy", wealthTrait)
	}
}

func TestFinalizeCharacterAuditAddsMissingPrereqAndTrimsBackgroundSkills(t *testing.T) {
	entity := gurps.NewEntity()
	entity.TotalPoints = fxp.FromInteger(6)

	alchemy := gurps.NewSkill(entity, nil, false)
	alchemy.Name = "Alchemy"
	alchemy.SetRawPoints(fxp.FromInteger(4))
	alchemy.Prereq = gurps.NewPrereqList()
	alchemyPrereq := gurps.NewSkillPrereq()
	alchemyPrereq.NameCriteria.Compare = criteria.IsText
	alchemyPrereq.NameCriteria.Qualifier = "Thaumatology"
	alchemyPrereq.LevelCriteria.Compare = criteria.AtLeastNumber
	alchemyPrereq.LevelCriteria.Qualifier = fxp.One
	alchemy.Prereq.Prereqs = gurps.Prereqs{alchemyPrereq}

	cooking := gurps.NewSkill(entity, nil, false)
	cooking.Name = "Cooking"
	cooking.SetRawPoints(fxp.FromInteger(2))

	entity.SetSkillList([]*gurps.Skill{alchemy, cooking})
	entity.Recalculate()

	summary := aiFinalizeCharacterAuditDetailed(entity, 6)
	thaumatology := entity.BestSkillNamed("Thaumatology", "", false, nil)
	if thaumatology == nil {
		t.Fatal("expected missing prerequisite skill to be added")
	}
	if got := fxp.AsInteger[int](thaumatology.Points); got != 1 {
		t.Fatalf("expected Thaumatology to be added at 1 point, got %d", got)
	}
	if got := fxp.AsInteger[int](cooking.Points); got != 1 {
		t.Fatalf("expected Cooking to be trimmed to 1 point, got %d", got)
	}
	if summary.UnspentCP != 0 {
		t.Fatalf("expected final audit to end on budget, got unspent %d", summary.UnspentCP)
	}
	if len(summary.AddedPrerequisiteSkills) != 1 || summary.AddedPrerequisiteSkills[0] != "Thaumatology" {
		t.Fatalf("expected Thaumatology to be reported as an added prerequisite skill, got %#v", summary.AddedPrerequisiteSkills)
	}
	if len(summary.TrimmedBackgroundSkills) == 0 || !strings.Contains(summary.TrimmedBackgroundSkills[0], "Cooking") {
		t.Fatalf("expected Cooking to be reported as a trimmed background skill, got %#v", summary.TrimmedBackgroundSkills)
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
	resolved, err := dockable.executeLocalCorrectionLoop("http://local", "test-model", "system", "Step 4: Advantages & Perks", resolution, aiAdvantagesOnlyActionPlan)
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
	resolved, err := dockable.executeLocalCorrectionLoop("http://local", "test-model", "system", "Step 4: Advantages & Perks", resolution, aiAdvantagesOnlyActionPlan)
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
	resolved, err := dockable.executeLocalCorrectionLoop("http://local", "test-model", "system", "Step 6: Equipment", resolution, aiEquipmentOnlyActionPlan)
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
	resolved, err := dockable.executeLocalCorrectionLoop("http://local", "test-model", "system", "Step 4: Advantages & Perks", resolution, aiAdvantagesOnlyActionPlan)
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
	if !strings.Contains(joinedWarnings, "ignored 1 unrelated correction entries") {
		t.Fatalf("expected unrelated correction warning, got %#v", resolved.Warnings)
	}
	if !strings.Contains(joinedWarnings, "made no progress and was stopped") {
		t.Fatalf("expected no-progress warning after filtering unrelated entries, got %#v", resolved.Warnings)
	}
}
