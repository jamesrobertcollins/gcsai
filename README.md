# GURPS Character Sheet

[![Go Reference](https://pkg.go.dev/badge/github.com/richardwilkes/gcs/v5.svg)](https://pkg.go.dev/github.com/richardwilkes/gcs/v5)
[![Go Report Card](https://goreportcard.com/badge/github.com/richardwilkes/gcs/v5)](https://goreportcard.com/report/github.com/richardwilkes/gcs/v5)

GURPS[^1] Character Sheet (GCS) is a stand-alone, interactive, character sheet editor that allows you to build
characters for the [GURPS Fourth Edition](http://www.sjgames.com/gurps) roleplaying game system.

GCS relies on another project of mine, [Unison](https://github.com/richardwilkes/unison),
for the UI and OS integration. The [prerequisites](https://github.com/richardwilkes/unison/blob/main/README.md) are
therefore the same as for that project. Once you have the prerequistes, you can build GCS by running the build script:
`./build.sh`. Add a `-h` to see available options.

## Local AI Model Workflow

When working with the local Ollama-backed GURPS model, rebuild and evaluate it from this repo root.

Windows PowerShell build:

```powershell
$env:GOEXPERIMENT='jsonv2'
go build -o gcs.exe .
```

Rebuild the Ollama model after changing [ai_model/Modelfile](ai_model/Modelfile):

```powershell
ollama create gurps-state-machine:latest -f ai_model/Modelfile
```

Run the baseline edit probes:

```powershell
$env:GOEXPERIMENT='jsonv2'
go run ./cmd/ai-baseline-eval -endpoint http://localhost:11434 -model gurps-state-machine:latest -show-responses
```

Run the broader local contract harness:

```powershell
$env:GOEXPERIMENT='jsonv2'
go run ./cmd/ai-contract-harness -endpoint http://localhost:11434 -model gurps-state-machine:latest -show-responses
```

The app includes a compatibility shim that is intentionally limited to `gurps-state-machine*` model names. Other local models continue using the strict parser and validator path unchanged.

[^1]: GURPS is a trademark of Steve Jackson Games, and its rules and art are copyrighted by Steve Jackson Games. All
rights are reserved by Steve Jackson Games. This game aid is the original creation of Richard A. Wilkes and is
released for free distribution, and not for resale, under the permissions granted in the
[Steve Jackson Games Online Policy](http://www.sjgames.com/general/online_policy.html).
