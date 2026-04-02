package ux

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/richardwilkes/gcs/v5/model/fxp"
	"github.com/richardwilkes/gcs/v5/model/gurps"
	"github.com/richardwilkes/toolbox/v2/i18n"
	"github.com/richardwilkes/unison"
)

var aiAutoBalanceFallbackSkillNames = []string{
	"Hiking",
	"Observation",
	"Search",
	"First Aid",
	"Stealth",
	"Brawling",
}

type aiLocalGenerationPhaseApplyResult struct {
	RemainingCP int
	Summary     string
	Warnings    []string
	Err         error
}

func aiPhase1OnlyActionPlan(plan aiActionPlan) aiActionPlan {
	return aiActionPlan{
		Attributes:    append([]aiAttributeAction(nil), plan.Attributes...),
		Advantages:    append([]aiNamedAction(nil), plan.Advantages...),
		Disadvantages: append([]aiNamedAction(nil), plan.Disadvantages...),
		Quirks:        append([]aiNamedAction(nil), plan.Quirks...),
	}
}

func aiPhase2OnlyActionPlan(plan aiActionPlan) aiActionPlan {
	return aiActionPlan{
		Skills:    append([]aiSkillAction(nil), plan.Skills...),
		Equipment: append([]aiNamedAction(nil), plan.Equipment...),
	}
}

func aiSnapSkillPointsInPlan(plan aiActionPlan) aiActionPlan {
	if len(plan.Skills) == 0 {
		return plan
	}
	plan.Skills = append([]aiSkillAction(nil), plan.Skills...)
	for i, action := range plan.Skills {
		pointsText := strings.TrimSpace(firstNonEmptyString(action.Points.String(), action.Value.String()))
		if pointsText == "" {
			continue
		}
		requestedPoints, err := strconv.Atoi(pointsText)
		if err != nil {
			continue
		}
		plan.Skills[i].Points = aiFlexibleString(strconv.Itoa(SnapToValidSkillPoints(requestedPoints)))
		plan.Skills[i].Value = ""
	}
	return plan
}

func SnapToValidSkillPoints(requestedPoints int) int {
	if requestedPoints <= 1 {
		return 1
	}
	if requestedPoints == 2 {
		return 2
	}
	if requestedPoints <= 4 {
		if requestedPoints-2 <= 4-requestedPoints {
			return 2
		}
		return 4
	}
	lower := 4 * (requestedPoints / 4)
	upper := lower + 4
	if lower < 4 {
		lower = 4
	}
	if requestedPoints-lower <= upper-requestedPoints {
		return lower
	}
	return upper
}

func aiNextValidSkillPointCost(currentPoints int) int {
	if currentPoints <= 0 {
		return 1
	}
	if currentPoints == 1 {
		return 2
	}
	if currentPoints == 2 {
		return 4
	}
	if currentPoints < 4 {
		return 4
	}
	return currentPoints + 4
}

func aiLargestValidSkillPointCostNotExceeding(limit int) int {
	if limit <= 0 {
		return 0
	}
	if limit == 1 {
		return 1
	}
	if limit == 2 || limit == 3 {
		return 2
	}
	if limit <= 4 {
		return 4
	}
	return 4 * (limit / 4)
}

func (d *aiChatDockable) prepareLocalGenerationTarget(targetCP int) (string, error) {
	resultCh := make(chan struct {
		summary string
		err     error
	}, 1)
	unison.InvokeTask(func() {
		sheet := d.sheetOrCreateNew()
		if sheet == nil || sheet.entity == nil {
			resultCh <- struct {
				summary string
				err     error
			}{err: fmt.Errorf("no active sheet to apply changes to")}
			return
		}
		sheet.entity.TotalPoints = fxp.FromInteger(targetCP)
		sheet.entity.Recalculate()
		sheet.Rebuild(true)
		resultCh <- struct {
			summary string
			err     error
		}{summary: d.currentCharacterSummary()}
	})
	result := <-resultCh
	return result.summary, result.err
}

func (d *aiChatDockable) resolveFilteredAIResponseText(responseText string, filter func(aiActionPlan) aiActionPlan) (aiPlanResolutionResult, error) {
	plan, ok := d.parseAIActionPlan(responseText)
	if !ok {
		return aiPlanResolutionResult{Parsed: false}, nil
	}
	filtered := filter(plan)
	if !hasAIActionPlanContent(filtered) {
		return aiPlanResolutionResult{Parsed: true, Plan: filtered, ResolvedPlan: filtered}, nil
	}
	resolution, err := d.resolveAIActionPlanResult(filtered)
	if err != nil {
		return aiPlanResolutionResult{}, err
	}
	resolution.Plan = filtered
	return resolution, nil
}

func (d *aiChatDockable) applyLocalGenerationPhase(label, responseText string, resolution aiPlanResolutionResult, targetCP int) aiLocalGenerationPhaseApplyResult {
	resultCh := make(chan aiLocalGenerationPhaseApplyResult, 1)
	unison.InvokeTask(func() {
		message := responseText
		if strings.TrimSpace(label) != "" {
			message = fmt.Sprintf("%s\n%s", label, responseText)
		}
		d.addMessage("AI", message)
		if !resolution.Parsed {
			if strings.Contains(responseText, "{") {
				d.addMessage("AI", i18n.Text("Structured update data was detected, but it could not be parsed into a character-sheet update."))
			}
			resultCh <- aiLocalGenerationPhaseApplyResult{Err: fmt.Errorf("%s could not be parsed", label)}
			return
		}

		sheet := d.sheetOrCreateNew()
		if sheet == nil || sheet.entity == nil {
			resultCh <- aiLocalGenerationPhaseApplyResult{Err: fmt.Errorf("no active sheet to apply changes to")}
			return
		}
		entity := sheet.entity
		entity.TotalPoints = fxp.FromInteger(targetCP)

		warnings := append([]string(nil), resolution.Warnings...)
		if hasAIActionPlanContent(resolution.ResolvedPlan) {
			applyWarnings, applyRetryItems, applyErr := d.applyAIActionPlan(resolution.ResolvedPlan)
			if applyErr != nil {
				resultCh <- aiLocalGenerationPhaseApplyResult{Err: applyErr}
				return
			}
			warnings = append(warnings, applyWarnings...)
			if len(applyRetryItems) != 0 {
				warnings = append(warnings, i18n.Text("Warning: some requested items still needed exact library selection and were skipped."))
			}
		} else {
			entity.Recalculate()
			sheet.Rebuild(true)
		}
		if len(resolution.RetryItems) != 0 {
			warnings = append(warnings, i18n.Text("Warning: some requested items could not be resolved to exact library entries and were skipped."))
		}
		for _, warning := range warnings {
			d.addMessage("AI", warning)
		}
		remainingCP := fxp.AsInteger[int](entity.UnspentPoints())
		d.addMessage("AI", fmt.Sprintf(i18n.Text("%s complete. Remaining CP: %d."), label, remainingCP))
		resultCh <- aiLocalGenerationPhaseApplyResult{
			RemainingCP: remainingCP,
			Summary:     d.currentCharacterSummary(),
			Warnings:    warnings,
		}
	})
	return <-resultCh
}

func (d *aiChatDockable) autoBalanceLocalGeneration(targetCP int) (before, after int, err error) {
	resultCh := make(chan struct {
		before int
		after  int
		err    error
	}, 1)
	unison.InvokeTask(func() {
		sheet := d.sheetOrCreateNew()
		if sheet == nil || sheet.entity == nil {
			resultCh <- struct {
				before int
				after  int
				err    error
			}{err: fmt.Errorf("no active sheet to apply changes to")}
			return
		}
		entity := sheet.entity
		entity.TotalPoints = fxp.FromInteger(targetCP)
		entity.Recalculate()
		before = fxp.AsInteger[int](entity.UnspentPoints())
		AutoBalanceUnspentPoints(entity, targetCP)
		entity.Recalculate()
		sheet.Rebuild(true)
		ActivateDockable(sheet)
		MarkModified(sheet)
		after = fxp.AsInteger[int](entity.UnspentPoints())
		resultCh <- struct {
			before int
			after  int
			err    error
		}{before: before, after: after}
	})
	result := <-resultCh
	return result.before, result.after, result.err
}

func (d *aiChatDockable) executeLocalThreePhaseGeneration(endpoint, model, originalPrompt string, params aiCharacterRequestParams) {
	targetCP := params.TotalCP
	phase1Summary, err := d.prepareLocalGenerationTarget(targetCP)
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI build could not be started: %v"), err))
		})
		return
	}

	phase1SystemPrompt, phase1UserPrompt := aiBuildLocalPhase1Prompts(originalPrompt, params, phase1Summary)
	writeSystemPromptDebugFile(phase1SystemPrompt)
	phase1Response, err := d.queryLocalModel(endpoint, model, buildLocalStatelessMessages(phase1SystemPrompt, phase1UserPrompt), aiActionPlanJSONSchema())
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", err.Error())
		})
		return
	}
	phase1Resolution, err := d.resolveFilteredAIResponseText(phase1Response, aiPhase1OnlyActionPlan)
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", phase1Response)
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Phase 1 could not be resolved: %v"), err))
		})
		return
	}
	phase1Apply := d.applyLocalGenerationPhase(i18n.Text("Phase 1: Core Chassis"), phase1Response, phase1Resolution, targetCP)
	if phase1Apply.Err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Phase 1 could not be applied: %v"), phase1Apply.Err))
		})
		return
	}

	remainingCP := phase1Apply.RemainingCP
	if remainingCP > 0 {
		phase2SystemPrompt, phase2UserPrompt := aiBuildLocalPhase2Prompts(originalPrompt, params, remainingCP, phase1Apply.Summary)
		writeSystemPromptDebugFile(phase2SystemPrompt)
		phase2Response, phase2Err := d.queryLocalModel(endpoint, model, buildLocalStatelessMessages(phase2SystemPrompt, phase2UserPrompt), aiActionPlanJSONSchema())
		if phase2Err != nil {
			unison.InvokeTask(func() {
				d.addMessage("AI", phase2Err.Error())
			})
			return
		}
		phase2Resolution, resolveErr := d.resolveFilteredAIResponseText(phase2Response, func(plan aiActionPlan) aiActionPlan {
			return aiSnapSkillPointsInPlan(aiPhase2OnlyActionPlan(plan))
		})
		if resolveErr != nil {
			unison.InvokeTask(func() {
				d.addMessage("AI", phase2Response)
				d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Phase 2 could not be resolved: %v"), resolveErr))
			})
			return
		}
		phase2Apply := d.applyLocalGenerationPhase(i18n.Text("Phase 2: Professional Package"), phase2Response, phase2Resolution, targetCP)
		if phase2Apply.Err != nil {
			unison.InvokeTask(func() {
				d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Phase 2 could not be applied: %v"), phase2Apply.Err))
			})
			return
		}
		remainingCP = phase2Apply.RemainingCP
	}

	beforeBalance, afterBalance, balanceErr := d.autoBalanceLocalGeneration(targetCP)
	if balanceErr != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Phase 3 could not be applied: %v"), balanceErr))
		})
		return
	}
	unison.InvokeTask(func() {
		if beforeBalance > 0 && afterBalance == 0 {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("Phase 3: Auto-Balancer spent the remaining %d CP and finished at exactly 0 unspent."), beforeBalance))
		} else if afterBalance == 0 {
			d.addMessage("AI", i18n.Text("Phase 3: Auto-Balancer confirmed the build is already at exactly 0 unspent CP."))
		} else if afterBalance > 0 {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("Phase 3: Auto-Balancer reduced the remainder from %d CP to %d CP, but could not reach exactly 0."), beforeBalance, afterBalance))
		} else {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("Phase 3: build is overspent by %d CP after deterministic balancing."), -afterBalance))
		}
		if afterBalance == 0 {
			d.addMessage("AI", i18n.Text("AI plan has been applied to the active character sheet."))
		}
	})
}

func AutoBalanceUnspentPoints(entity *gurps.Entity, targetCP int) {
	if entity == nil || targetCP <= 0 {
		return
	}
	entity.TotalPoints = fxp.FromInteger(targetCP)
	entity.Recalculate()
	for i := 0; i < 64; i++ {
		remaining := fxp.AsInteger[int](entity.UnspentPoints())
		if remaining <= 0 {
			break
		}
		if autoBalanceIncrementHighestSkill(entity, remaining) {
			entity.Recalculate()
			continue
		}
		if autoBalanceAddFit(entity, remaining) {
			entity.Recalculate()
			continue
		}
		if autoBalanceAddFallbackSkill(entity, remaining) {
			entity.Recalculate()
			continue
		}
		break
	}
	entity.Recalculate()
}

func autoBalanceIncrementHighestSkill(entity *gurps.Entity, remaining int) bool {
	if entity == nil || remaining <= 0 {
		return false
	}
	skills := make([]*gurps.Skill, 0, len(entity.Skills))
	for _, skill := range entity.Skills {
		if skill == nil || skill.Container() {
			continue
		}
		skills = append(skills, skill)
	}
	if len(skills) == 0 {
		return false
	}
	sort.Slice(skills, func(i, j int) bool {
		leftLevel := skills[i].CalculateLevel(nil).Level
		rightLevel := skills[j].CalculateLevel(nil).Level
		if leftLevel != rightLevel {
			return leftLevel > rightLevel
		}
		leftPoints := skills[i].Points
		rightPoints := skills[j].Points
		if leftPoints != rightPoints {
			return leftPoints > rightPoints
		}
		return aiResolvedSkillDisplayName(skills[i]) < aiResolvedSkillDisplayName(skills[j])
	})
	for _, skill := range skills {
		currentPoints := fxp.AsInteger[int](skill.Points)
		nextPoints := aiNextValidSkillPointCost(currentPoints)
		increment := nextPoints - currentPoints
		if increment <= 0 || increment > remaining {
			continue
		}
		skill.SetRawPoints(fxp.FromInteger(nextPoints))
		return true
	}
	return false
}

func autoBalanceAddFit(entity *gurps.Entity, remaining int) bool {
	if entity == nil || remaining < 5 {
		return false
	}
	before := entity.UnspentPoints()
	var d aiChatDockable
	traits := append([]*gurps.Trait(nil), entity.Traits...)
	updatedTraits, _, err := d.addOrUpdateTrait(entity, traits, aiNamedAction{Name: aiFlexibleString("Fit"), Points: aiFlexibleString("5")})
	if err != nil {
		return false
	}
	entity.SetTraitList(updatedTraits)
	entity.Recalculate()
	return entity.UnspentPoints() < before
}

func autoBalanceAddFallbackSkill(entity *gurps.Entity, remaining int) bool {
	if entity == nil || remaining <= 0 {
		return false
	}
	desiredPoints := aiLargestValidSkillPointCostNotExceeding(remaining)
	if desiredPoints <= 0 {
		return false
	}
	var d aiChatDockable
	for _, name := range aiAutoBalanceFallbackSkillNames {
		before := entity.UnspentPoints()
		skills := append([]*gurps.Skill(nil), entity.Skills...)
		updatedSkills, _, retryItem, err := d.addOrUpdateSkill(entity, skills, aiSkillAction{Name: aiFlexibleString(name), Points: aiFlexibleString(strconv.Itoa(desiredPoints))})
		if err != nil || retryItem != nil {
			continue
		}
		entity.SetSkillList(updatedSkills)
		entity.Recalculate()
		if entity.UnspentPoints() < before {
			return true
		}
	}
	return false
}
