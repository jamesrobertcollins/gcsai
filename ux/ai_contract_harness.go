package ux

import (
	"fmt"
	"strings"
)

type AILocalContractCaseReport struct {
	Name     string
	Passed   bool
	Error    string
	Response string
}

type AILocalContractHarnessReport struct {
	Endpoint string
	Model    string
	Cases    []AILocalContractCaseReport
}

func (r AILocalContractHarnessReport) PassedCount() int {
	count := 0
	for _, testCase := range r.Cases {
		if testCase.Passed {
			count++
		}
	}
	return count
}

func (r AILocalContractHarnessReport) FailedCount() int {
	return len(r.Cases) - r.PassedCount()
}

func (r AILocalContractHarnessReport) ScorePercent() int {
	if len(r.Cases) == 0 {
		return 0
	}
	return r.PassedCount() * 100 / len(r.Cases)
}

type aiLocalContractCase struct {
	Name         string
	SystemPrompt string
	UserPrompt   string
	Schema       any
	Validate     func(string, string) error
}

var (
	aiHarnessQueryModel = func(endpoint, model string, messages []aiLocalChatMessage, schema any) (string, error) {
		return aiQueryLocalModelText(endpoint, model, messages, schema, nil)
	}
	aiHarnessResolvePlan = func(plan aiActionPlan) (aiPlanResolutionResult, error) {
		var dockable aiChatDockable
		return dockable.resolveAIActionPlanResult(plan)
	}
)

func RunLocalAIContractHarness(endpoint, model string) (AILocalContractHarnessReport, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return AILocalContractHarnessReport{}, fmt.Errorf("local AI endpoint is required")
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "http://" + endpoint
	}
	endpoint = strings.TrimSuffix(endpoint, "/")
	model = strings.TrimSpace(model)
	if model == "" {
		return AILocalContractHarnessReport{}, fmt.Errorf("local AI model is required")
	}
	report := AILocalContractHarnessReport{Endpoint: endpoint, Model: model}
	for _, contractCase := range aiBuildLocalContractCases() {
		result := AILocalContractCaseReport{Name: contractCase.Name}
		response, err := aiHarnessQueryModel(endpoint, model, buildLocalStatelessMessages(contractCase.SystemPrompt, contractCase.UserPrompt), contractCase.Schema)
		result.Response = response
		if err != nil {
			result.Error = err.Error()
			report.Cases = append(report.Cases, result)
			continue
		}
		if validateErr := contractCase.Validate(model, response); validateErr != nil {
			result.Error = validateErr.Error()
			report.Cases = append(report.Cases, result)
			continue
		}
		result.Passed = true
		report.Cases = append(report.Cases, result)
	}
	return report, nil
}

func aiBuildLocalContractCases() []aiLocalContractCase {
	params := aiCharacterRequestParams{
		TotalCP:           150,
		TechLevel:         "8",
		Concept:           "modern assassin focused on knives and poison",
		StartingWealth:    20000,
		DisadvantageLimit: 50,
	}
	draft := aiDraftProfile{
		CharacterConcept: aiFlexibleString("modern assassin focused on knives and poison"),
		Name:             aiFlexibleString("Wong Jick"),
		TechLevel:        aiFlexibleString("8"),
		CPLimit:          aiFlexibleString("150"),
		StartingWealth:   aiFlexibleString("$20,000"),
	}
	approvedBaseline := aiFormatDraftProfileForPrompt(draft, true)
	themes := []string{"Assassin", "Knife Fighter", "Poisoner"}
	vocabulary := "Thematic Canonical GURPS Vocabulary:\n- Disadvantages: Code of Honor, Enemy, Bloodlust\n- Quirks: Always sharpens blades, Keeps a poison ledger\n- Advantages: Combat Reflexes, Signature Gear, Language Talent\n- Skills: Knife, Stealth, Fast-Talk, Holdout, Poisons, Shadowing\n- Equipment: Large Knife, Small Knife, Lockpicks, Poison Kit, Black Bodysuit"
	budget := aiDefaultGenerationBudget(params.TotalCP)

	baselineSystemPrompt := aiLocalBaselineGatheringSystemPrompt(draft)
	blueprintSystemPrompt, blueprintUserPrompt := aiBuildLocalBlueprintPrompts(approvedBaseline, params)
	storySystemPrompt, storyUserPrompt := aiBuildLocalStoryEnginePrompts(approvedBaseline, params, themes, aiFilterThematicVocabularySections(vocabulary, "Disadvantages", "Quirks"))
	attributeSystemPrompt, attributeUserPrompt := aiBuildLocalAttributePrompts(approvedBaseline, params, themes, budget.Attributes, "Current character sheet context: baseline approved; no traits applied yet.")
	advantageSystemPrompt, advantageUserPrompt := aiBuildLocalAdvantagesPrompts(approvedBaseline, params, themes, budget.Advantages, "Current character sheet context: baseline approved; disadvantages and attributes already applied.", aiFilterThematicVocabularySections(vocabulary, "Advantages"))
	skillsSystemPrompt, skillsUserPrompt := aiBuildLocalSkillsPrompts(approvedBaseline, params, themes, budget, "Current character sheet context: baseline approved; traits and attributes already applied.", aiFilterThematicVocabularySections(vocabulary, "Skills", "Spells"))
	equipmentSystemPrompt, equipmentUserPrompt := aiBuildLocalEquipmentPrompts(approvedBaseline, params, themes, params.StartingWealth, "Current character sheet context: baseline approved; traits, attributes, and skills already applied.", aiFilterThematicVocabularySections(vocabulary, "Equipment"))

	return []aiLocalContractCase{
		{
			Name:         "Baseline Draft Profile",
			SystemPrompt: baselineSystemPrompt,
			UserPrompt:   "Randomize the remaining blank profile details, leave religion blank, and return complete draft_profile JSON only.",
			Schema:       aiLocalBaselineDraftProfileJSONSchema(),
			Validate:     aiValidateBaselineContractResponse,
		},
		{
			Name:         "Step 1 Blueprint",
			SystemPrompt: blueprintSystemPrompt,
			UserPrompt:   blueprintUserPrompt,
			Schema:       aiGenerationBlueprintJSONSchema(),
			Validate: func(_ string, text string) error {
				_, _, err := aiParseGenerationBlueprintResponse(text, params.TotalCP, params.Concept)
				return err
			},
		},
		{
			Name:         "Step 2 Story Engine",
			SystemPrompt: storySystemPrompt,
			UserPrompt:   storyUserPrompt,
			Schema:       aiActionPlanJSONSchema(),
			Validate: func(model, text string) error {
				return aiValidateActionPlanContractResponseForModel(model, text, map[string]bool{"disadvantages": true, "quirks": true})
			},
		},
		{
			Name:         "Step 3 Attributes",
			SystemPrompt: attributeSystemPrompt,
			UserPrompt:   attributeUserPrompt,
			Schema:       aiActionPlanJSONSchema(),
			Validate: func(model, text string) error {
				return aiValidateActionPlanContractResponseForModel(model, text, map[string]bool{"attributes": true})
			},
		},
		{
			Name:         "Step 4 Advantages",
			SystemPrompt: advantageSystemPrompt,
			UserPrompt:   advantageUserPrompt,
			Schema:       aiActionPlanJSONSchema(),
			Validate: func(model, text string) error {
				return aiValidateActionPlanContractResponseForModel(model, text, map[string]bool{"advantages": true})
			},
		},
		{
			Name:         "Step 5 Skills & Spells",
			SystemPrompt: skillsSystemPrompt,
			UserPrompt:   skillsUserPrompt,
			Schema:       aiActionPlanJSONSchema(),
			Validate: func(model, text string) error {
				return aiValidateActionPlanContractResponseForModel(model, text, map[string]bool{"skills": true, "spells": true})
			},
		},
		{
			Name:         "Step 6 Equipment",
			SystemPrompt: equipmentSystemPrompt,
			UserPrompt:   equipmentUserPrompt,
			Schema:       aiActionPlanJSONSchema(),
			Validate: func(model, text string) error {
				return aiValidateActionPlanContractResponseForModel(model, text, map[string]bool{"equipment": true})
			},
		},
	}
}

func aiValidateBaselineContractResponse(model, text string) error {
	if err := aiValidateLocalBaselineCollectionResponseTextForModel(text, model); err != nil {
		return err
	}
	response, ok := aiParseLocalBaselineDraftProfileResponseForModel(text, model)
	if !ok {
		return fmt.Errorf("baseline contract response did not parse as draft_profile JSON")
	}
	if !strings.EqualFold(strings.TrimSpace(response.Status.String()), "complete") {
		return fmt.Errorf("baseline contract response did not finish baseline collection; expected status=complete, got %q", response.Status.String())
	}
	profile := aiNormalizeDraftProfile(response.DraftProfile)
	if strings.TrimSpace(profile.CharacterConcept.String()) == "" || strings.TrimSpace(profile.TechLevel.String()) == "" || strings.TrimSpace(profile.CPLimit.String()) == "" {
		return fmt.Errorf("baseline contract response omitted required draft_profile fields")
	}
	return nil
}

func aiValidateActionPlanContractResponse(text string, allowed map[string]bool) error {
	return aiValidateActionPlanContractResponseForModel("", text, allowed)
}

func aiValidateActionPlanContractResponseForModel(model, text string, allowed map[string]bool) error {
	var dockable aiChatDockable
	plan, ok := dockable.parseAIActionPlanForModel(text, model)
	if !ok {
		return fmt.Errorf("response did not parse as a character-sheet action plan")
	}
	if unexpected := aiUnexpectedActionPlanSections(plan, allowed); len(unexpected) != 0 {
		return fmt.Errorf("response included disallowed sections: %s", strings.Join(unexpected, ", "))
	}
	if !aiActionPlanContainsAllowedContent(plan, allowed) {
		return fmt.Errorf("response did not include any allowed sections")
	}
	resolution, err := aiHarnessResolvePlan(plan)
	if err != nil {
		return fmt.Errorf("response could not be resolved against the GURPS library: %w", err)
	}
	if len(resolution.RetryItems) != 0 {
		return fmt.Errorf("response included unresolved library items: %s", aiRetryItemsSummary(resolution.RetryItems))
	}
	return nil
}

func aiUnexpectedActionPlanSections(plan aiActionPlan, allowed map[string]bool) []string {
	var sections []string
	if plan.Profile != nil && !allowed["profile"] {
		sections = append(sections, "profile")
	}
	if len(plan.Attributes) != 0 && !allowed["attributes"] {
		sections = append(sections, "attributes")
	}
	if len(plan.Advantages) != 0 && !allowed["advantages"] {
		sections = append(sections, "advantages")
	}
	if len(plan.Disadvantages) != 0 && !allowed["disadvantages"] {
		sections = append(sections, "disadvantages")
	}
	if len(plan.Quirks) != 0 && !allowed["quirks"] {
		sections = append(sections, "quirks")
	}
	if len(plan.Skills) != 0 && !allowed["skills"] {
		sections = append(sections, "skills")
	}
	if len(plan.Spells) != 0 && !allowed["spells"] {
		sections = append(sections, "spells")
	}
	if len(plan.Equipment) != 0 && !allowed["equipment"] {
		sections = append(sections, "equipment")
	}
	if plan.SpendAllCP && !allowed["spend_all_cp"] {
		sections = append(sections, "spend_all_cp")
	}
	return sections
}

func aiActionPlanContainsAllowedContent(plan aiActionPlan, allowed map[string]bool) bool {
	return (allowed["profile"] && plan.Profile != nil) ||
		(allowed["attributes"] && len(plan.Attributes) != 0) ||
		(allowed["advantages"] && len(plan.Advantages) != 0) ||
		(allowed["disadvantages"] && len(plan.Disadvantages) != 0) ||
		(allowed["quirks"] && len(plan.Quirks) != 0) ||
		(allowed["skills"] && len(plan.Skills) != 0) ||
		(allowed["spells"] && len(plan.Spells) != 0) ||
		(allowed["equipment"] && len(plan.Equipment) != 0) ||
		(allowed["spend_all_cp"] && plan.SpendAllCP)
}
