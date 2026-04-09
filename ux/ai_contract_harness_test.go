package ux

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunLocalAIContractHarness(t *testing.T) {
	previousResolvePlan := aiHarnessResolvePlan
	aiHarnessResolvePlan = func(plan aiActionPlan) (aiPlanResolutionResult, error) {
		return aiPlanResolutionResult{Parsed: true, Plan: plan, ResolvedPlan: plan}, nil
	}
	t.Cleanup(func() {
		aiHarnessResolvePlan = previousResolvePlan
	})

	responses := []string{
		`{"status":"complete","draft_profile":{"character_concept":"modern assassin focused on knives and poison","name":"Wong Jick","age":"37","height":"5'10\"","weight":"175 lbs","tech_level":"8","cp_limit":"150","starting_wealth":"$20,000","world_setting":"Modern action thriller"}}`,
		`{"themes":["Assassin","Knife Fighter","Poisoner"],"budget_percentages":{"attributes":40,"advantages":20,"core_skills":25,"background_skills":15}}`,
		`{"disadvantages":[{"name":"Code of Honor","points":"-10"}],"quirks":[{"name":"Always sharpens blades","points":"-1"}]}`,
		`{"attributes":[{"id":"DX","value":"14"},{"id":"IQ","value":"12"}]}`,
		`{"advantages":[{"name":"Combat Reflexes","points":"15"}]}`,
		`{"skills":[{"name":"Knife","points":"4"},{"name":"Stealth","points":"4"}],"spells":[]}`,
		`{"equipment":[{"name":"Large Knife","quantity":2},{"name":"Poison Kit","quantity":1}]}`,
	}
	callIndex := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		if callIndex >= len(responses) {
			t.Fatalf("unexpected extra harness request %d", callIndex)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"content": responses[callIndex]},
		})
		callIndex++
	}))
	defer server.Close()

	report, err := RunLocalAIContractHarness(server.URL, "test-model")
	if err != nil {
		t.Fatalf("expected harness to run successfully, got %v", err)
	}
	if len(report.Cases) != len(responses) {
		t.Fatalf("expected %d harness cases, got %d", len(responses), len(report.Cases))
	}
	if report.PassedCount() != len(responses) {
		t.Fatalf("expected all harness cases to pass, got %#v", report)
	}
	if report.ScorePercent() != 100 {
		t.Fatalf("expected perfect score, got %d", report.ScorePercent())
	}
}

func TestRunLocalAIContractHarnessFallsBackToGenerateEndpointAndChoices(t *testing.T) {
	previousResolvePlan := aiHarnessResolvePlan
	aiHarnessResolvePlan = func(plan aiActionPlan) (aiPlanResolutionResult, error) {
		return aiPlanResolutionResult{Parsed: true, Plan: plan, ResolvedPlan: plan}, nil
	}
	t.Cleanup(func() {
		aiHarnessResolvePlan = previousResolvePlan
	})

	responses := []string{
		`{"choices":[{"message":{"content":"{\"status\":\"complete\",\"draft_profile\":{\"character_concept\":\"modern assassin focused on knives and poison\",\"name\":\"Wong Jick\",\"tech_level\":\"8\",\"cp_limit\":\"150\",\"starting_wealth\":\"$20,000\",\"world_setting\":\"Modern action thriller\"}}"}}]}`,
		`{"choices":[{"text":"{\"themes\":[\"Assassin\",\"Knife Fighter\",\"Poisoner\"],\"budget_percentages\":{\"attributes\":40,\"advantages\":20,\"core_skills\":25,\"background_skills\":15}}"}]}`,
		`{"choices":[{"message":{"content":"{\"disadvantages\":[{\"name\":\"Code of Honor\",\"points\":\"-10\"}],\"quirks\":[{\"name\":\"Always sharpens blades\",\"points\":\"-1\"}]}"}}]}`,
		`{"choices":[{"message":{"content":"{\"attributes\":[{\"id\":\"DX\",\"value\":\"14\"}]}"}}]}`,
		`{"choices":[{"message":{"content":"{\"advantages\":[{\"name\":\"Combat Reflexes\",\"points\":\"15\"}]}"}}]}`,
		`{"choices":[{"message":{"content":"{\"skills\":[{\"name\":\"Knife\",\"points\":\"4\"}],\"spells\":[]}"}}]}`,
		`{"choices":[{"message":{"content":"{\"equipment\":[{\"name\":\"Large Knife\",\"quantity\":2}]}"}}]}`,
	}
	callIndex := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat":
			http.NotFound(w, r)
			return
		case "/api/generate":
			if callIndex >= len(responses) {
				t.Fatalf("unexpected extra harness request %d", callIndex)
			}
			_, _ = w.Write([]byte(responses[callIndex]))
			callIndex++
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := RunLocalAIContractHarness(server.URL, "test-model")
	if err != nil {
		t.Fatalf("expected harness fallback to succeed, got %v", err)
	}
	if report.FailedCount() != 0 {
		t.Fatalf("expected fallback harness to pass, got %#v", report)
	}
	if callIndex != len(responses) {
		t.Fatalf("expected %d /api/generate calls, got %d", len(responses), callIndex)
	}
}

func TestAIValidateActionPlanContractResponseRequiresResolverSuccess(t *testing.T) {
	previousResolvePlan := aiHarnessResolvePlan
	aiHarnessResolvePlan = func(plan aiActionPlan) (aiPlanResolutionResult, error) {
		return aiPlanResolutionResult{Parsed: true, Plan: plan, RetryItems: []aiRetryItem{{Category: string(aiLibraryCategoryAdvantage), Name: "Noncanonical Trait"}}}, nil
	}
	t.Cleanup(func() {
		aiHarnessResolvePlan = previousResolvePlan
	})

	err := aiValidateActionPlanContractResponse(`{"advantages":[{"name":"Noncanonical Trait","points":"5"}]}`, map[string]bool{"advantages": true})
	if err == nil {
		t.Fatal("expected unresolved resolver items to fail contract validation")
	}
	if !strings.Contains(err.Error(), "unresolved library items") {
		t.Fatalf("expected resolver validation error, got %q", err.Error())
	}
}

func TestAIValidateBaselineContractResponseUsesStateMachineAliasShimOnlyForTargetModel(t *testing.T) {
	response := `{"status":"complete","draft_profile":{"character_concept":"modern assassin","name_of_player_character":"Wong Jick","tech_level_of_game_world":8,"cp_limit_of_player_character":150,"starting_wealth_of_player_character":"$20,000"}}`
	if err := aiValidateBaselineContractResponse("gurps-state-machine:latest", response); err != nil {
		t.Fatalf("expected gurps-state-machine alias shim to accept baseline response, got %v", err)
	}
	if err := aiValidateBaselineContractResponse("llama-3.1", response); err == nil {
		t.Fatal("expected non-target model to reject unsupported baseline aliases")
	}
}
