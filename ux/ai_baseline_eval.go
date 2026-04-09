package ux

import (
	"fmt"
	"strings"
)

type AILocalBaselineEvalCaseReport struct {
	Name       string
	UserPrompt string
	Field      string
	Expected   string
	Actual     string
	Passed     bool
	Error      string
	Response   string
	UsedShim   bool
}

type AILocalBaselineEvalReport struct {
	Endpoint string
	Model    string
	Cases    []AILocalBaselineEvalCaseReport
}

func (r AILocalBaselineEvalReport) PassedCount() int {
	count := 0
	for _, testCase := range r.Cases {
		if testCase.Passed {
			count++
		}
	}
	return count
}

func (r AILocalBaselineEvalReport) FailedCount() int {
	return len(r.Cases) - r.PassedCount()
}

func (r AILocalBaselineEvalReport) ScorePercent() int {
	if len(r.Cases) == 0 {
		return 0
	}
	return r.PassedCount() * 100 / len(r.Cases)
}

func RunLocalAIBaselineEval(endpoint, model string) (AILocalBaselineEvalReport, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return AILocalBaselineEvalReport{}, fmt.Errorf("local AI endpoint is required")
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "http://" + endpoint
	}
	endpoint = strings.TrimSuffix(endpoint, "/")
	model = strings.TrimSpace(model)
	if model == "" {
		return AILocalBaselineEvalReport{}, fmt.Errorf("local AI model is required")
	}
	draft := aiDraftProfile{
		CharacterConcept: aiFlexibleString("based on John Wick but I want his name to be Wong Jick because hes Asian"),
		Name:             aiFlexibleString("Wong Jick"),
		Title:            aiFlexibleString("Assassin"),
		TechLevel:        aiFlexibleString("3"),
		CPLimit:          aiFlexibleString("150"),
		StartingWealth:   aiFlexibleString("$1,000"),
	}
	probes := []struct {
		Name       string
		UserPrompt string
		Field      string
		Expected   string
		Getter     func(aiDraftProfile) string
	}{
		{Name: "Overwrite Tech Level", UserPrompt: "Tech Level 8", Field: "tech_level", Expected: "8", Getter: func(profile aiDraftProfile) string { return profile.TechLevel.String() }},
		{Name: "Set Hair Color", UserPrompt: "he has Black hair", Field: "hair_color", Expected: "Black", Getter: func(profile aiDraftProfile) string { return profile.HairColor.String() }},
		{Name: "Set Age", UserPrompt: "Age 37", Field: "age", Expected: "37", Getter: func(profile aiDraftProfile) string { return profile.Age.String() }},
	}
	systemPrompt := aiLocalBaselineGatheringSystemPrompt(draft)
	report := AILocalBaselineEvalReport{Endpoint: endpoint, Model: model}
	for _, probe := range probes {
		result := AILocalBaselineEvalCaseReport{Name: probe.Name, UserPrompt: probe.UserPrompt, Field: probe.Field, Expected: probe.Expected, UsedShim: aiUsesGURPSStateMachineBaselineShim(model)}
		response, err := aiQueryLocalModelText(endpoint, model, buildLocalStatelessMessages(systemPrompt, probe.UserPrompt), aiLocalBaselineDraftProfileJSONSchema(), nil)
		result.Response = response
		if err != nil {
			result.Error = err.Error()
			report.Cases = append(report.Cases, result)
			continue
		}
		if validateErr := aiValidateLocalBaselineCollectionResponseTextForModel(response, model); validateErr != nil {
			result.Error = validateErr.Error()
			report.Cases = append(report.Cases, result)
			continue
		}
		parsed, ok := aiParseLocalBaselineDraftProfileResponseForModel(response, model)
		if !ok {
			result.Error = "baseline response did not parse after validation"
			report.Cases = append(report.Cases, result)
			continue
		}
		result.Actual = strings.TrimSpace(probe.Getter(aiNormalizeDraftProfile(parsed.DraftProfile)))
		result.Passed = strings.EqualFold(result.Actual, probe.Expected)
		if !result.Passed {
			result.Error = fmt.Sprintf("expected %s=%q, got %q", result.Field, result.Expected, result.Actual)
		}
		report.Cases = append(report.Cases, result)
	}
	return report, nil
}
