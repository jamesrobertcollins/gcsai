package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/richardwilkes/gcs/v5/ux"
)

func main() {
	endpoint := flag.String("endpoint", "http://localhost:11434", "Local AI server endpoint")
	model := flag.String("model", "", "Local AI model name")
	showResponses := flag.Bool("show-responses", false, "Print the raw model response for each baseline probe")
	flag.Parse()

	report, err := ux.RunLocalAIBaselineEval(*endpoint, *model)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	fmt.Printf("Local AI baseline eval: %d/%d passed (%d%%)\n", report.PassedCount(), len(report.Cases), report.ScorePercent())
	fmt.Printf("Endpoint: %s\n", report.Endpoint)
	fmt.Printf("Model: %s\n", report.Model)
	for _, testCase := range report.Cases {
		status := "PASS"
		if !testCase.Passed {
			status = "FAIL"
		}
		fmt.Printf("[%s] %s\n", status, testCase.Name)
		fmt.Printf("  Prompt: %s\n", testCase.UserPrompt)
		fmt.Printf("  Field: %s | Expected: %s | Actual: %s\n", testCase.Field, testCase.Expected, testCase.Actual)
		if testCase.UsedShim {
			fmt.Printf("  Shim: gurps-state-machine baseline alias shim enabled\n")
		}
		if testCase.Error != "" {
			fmt.Printf("  %s\n", testCase.Error)
		}
		if *showResponses && strings.TrimSpace(testCase.Response) != "" {
			fmt.Printf("  Response: %s\n", strings.TrimSpace(testCase.Response))
		}
	}

	if report.FailedCount() != 0 {
		os.Exit(1)
	}
}
