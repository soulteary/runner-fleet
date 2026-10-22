# Go Report Card

**Grade: A+** (98.7%)

| Metric | Value |
| ------ | ----- |
| Files | 90 |
| Issues | 18 |

## Checks

| Check | Score |
| ----- | ----- |
| go_vet | 100% |
| gofmt | 100% |
| ineffassign | 100% |
| gocyclo | 88% |
| license | 100% |
| misspell | 91% |

## Issues

### gocyclo

- `internal/config/config.go`
  - Line 404: cyclomatic complexity 30 for function Validate
  - Line 195: cyclomatic complexity 19 for function (ResourceLimits).Validate
  - Line 343: cyclomatic complexity 17 for function Load
- `internal/handler/handler.go`
  - Line 377: cyclomatic complexity 27 for function AddRunner
  - Line 701: cyclomatic complexity 21 for function UpdateRunner
  - Line 102: cyclomatic complexity 16 for function runRegistrationJob
- `internal/runner/container.go`
  - Line 262: cyclomatic complexity 21 for function startRunnerContainer
- `internal/docsconsistency/envvars_test.go`
  - Line 43: cyclomatic complexity 20 for function TestEnvVars_EveryVariableTheCodeReadsIsDocumented
- `internal/runner/drift.go`
  - Line 201: cyclomatic complexity 19 for function (containerSpec).driftReason
- `internal/handler/precheck.go`
  - Line 92: cyclomatic complexity 17 for function collectRunnerConflicts
- `internal/secrets/guardrail_test.go`
  - Line 88: cyclomatic complexity 16 for function TestNoManagerSecretsUnderRunnerDirs
- `internal/docsconsistency/apikeys_test.go`
  - Line 29: cyclomatic complexity 23 for function TestAPIKeys_EveryKeyTheHandlerUsesExistsInAllLanguages
- `internal/runnerproc/runnerproc_test.go`
  - Line 180: cyclomatic complexity 17 for function TestFind_RealProcesses
- `internal/runner/githubstatus_test.go`
  - Line 12: cyclomatic complexity 17 for function TestGitHubStatus_RoundTripsThreeStates

### misspell

- `cmd/runner-manager/i18n/fr.json`
  - Line 23: "conteneurs" is a misspelling of "contenders"
  - Line 30: "marrage" is a misspelling of "marriage"
  - Line 31: "marrage" is a misspelling of "marriage"
  - Line 32: "marrage" is a misspelling of "marriage"
  - Line 41: "marrage" is a misspelling of "marriage"
  - Line 178: "marrage" is a misspelling of "marriage"
- `docs/de/README.md`
  - Line 7: "Konfiguration" is a misspelling of "Configuration"
- `docs/de/development.md`
  - Line 7: "Produktion" is a misspelling of "Production"
  - Line 62: "Konfiguration" is a misspelling of "Configuration"
  - Line 64: "Konfiguration" is a misspelling of "Configuration"
  - Line 107: "Konfiguration" is a misspelling of "Configuration"
  - Line 115: "Konfiguration" is a misspelling of "Configuration"
  - Line 116: "Konfiguration" is a misspelling of "Configuration"
  - Line 270: "Produktion" is a misspelling of "Production"
- `docs/fr/README.md`
  - Line 9: "conteneurs" is a misspelling of "contenders"
  - Line 10: "Superposer" is a misspelling of "Superpowers"
- `docs/fr/development.md`
  - Line 7: "contributeurs" is a misspelling of "contributes"
  - Line 63: "existant" is a misspelling of "existent"
  - Line 66: "marrage" is a misspelling of "marriage"
  - Line 113: "marrage" is a misspelling of "marriage"
  - Line 115: "conflit" is a misspelling of "conflict"
  - Line 117: "marrage" is a misspelling of "marriage"
  - Line 140: "constituer" is a misspelling of "constitute"
  - Line 152: "origines" is a misspelling of "origins"
  - Line 154: "origines" is a misspelling of "origins"
  - Line 160: "Exemple" is a misspelling of "Example"
  - Line 192: "marrage" is a misspelling of "marriage"
  - Line 199: "transforme" is a misspelling of "transfer"
  - Line 214: "existant" is a misspelling of "existent"
  - Line 244: "pointeur" is a misspelling of "pointer"
  - Line 244: "pointeur" is a misspelling of "pointer"
  - Line 260: "exemple" is a misspelling of "example"
  - Line 261: "construits" is a misspelling of "constructs"
  - Line 296: "pointeur" is a misspelling of "pointer"
  - Line 346: "marrage" is a misspelling of "marriage"
- `docs/fr/guide.md`
  - Line 13: "marrage" is a misspelling of "marriage"
  - Line 14: "marrage" is a misspelling of "marriage"
  - Line 16: "marrage" is a misspelling of "marriage"
  - Line 26: "marrage" is a misspelling of "marriage"
  - Line 43: "complets" is a misspelling of "completes"
  - Line 59: "marrage" is a misspelling of "marriage"
  - Line 74: "marrage" is a misspelling of "marriage"
  - Line 90: "normalis" is a misspelling of "normals"
  - Line 90: "conteneurs" is a misspelling of "contenders"
  - Line 90: "conflit" is a misspelling of "conflict"
  - Line 92: "conteneurs" is a misspelling of "contenders"
  - Line 92: "conteneurs" is a misspelling of "contenders"
  - Line 96: "conteneurs" is a misspelling of "contenders"
  - Line 96: "existant" is a misspelling of "existent"
  - Line 96: "marrage" is a misspelling of "marriage"
  - Line 102: "marrage" is a misspelling of "marriage"
  - Line 102: "marrage" is a misspelling of "marriage"
  - Line 105: "marrage" is a misspelling of "marriage"
  - Line 106: "marrage" is a misspelling of "marriage"
  - Line 108: "marrage" is a misspelling of "marriage"
  - Line 108: "conteneurs" is a misspelling of "contenders"
  - Line 142: "conteneurs" is a misspelling of "contenders"
  - Line 151: "conteneurs" is a misspelling of "contenders"
  - Line 151: "conteneurs" is a misspelling of "contenders"
  - Line 151: "marrage" is a misspelling of "marriage"
  - Line 157: "marrage" is a misspelling of "marriage"
  - Line 157: "marrage" is a misspelling of "marriage"
  - Line 179: "Origines" is a misspelling of "Origins"
  - Line 181: "marrage" is a misspelling of "marriage"
  - Line 194: "conteneurs" is a misspelling of "contenders"
  - Line 194: "marrage" is a misspelling of "marriage"
  - Line 194: "conteneurs" is a misspelling of "contenders"
  - Line 196: "Exemple" is a misspelling of "Example"
  - Line 233: "origines" is a misspelling of "origins"
  - Line 235: "normalis" is a misspelling of "normals"
  - Line 235: "conteneurs" is a misspelling of "contenders"
  - Line 237: "persistante" is a misspelling of "persistent"
  - Line 237: "conteneurs" is a misspelling of "contenders"
  - Line 237: "continuent" is a misspelling of "contingent"
  - Line 237: "marrage" is a misspelling of "marriage"
  - Line 243: "marrage" is a misspelling of "marriage"
  - Line 289: "conteneurs" is a misspelling of "contenders"
  - Line 291: "marrage" is a misspelling of "marriage"
  - Line 305: "marrage" is a misspelling of "marriage"
  - Line 312: "existant" is a misspelling of "existent"
  - Line 313: "marrage" is a misspelling of "marriage"
  - Line 329: "marrage" is a misspelling of "marriage"
  - Line 334: "restauration" is a misspelling of "restoration"
  - Line 337: "restauration" is a misspelling of "restoration"
  - Line 349: "davantage" is a misspelling of "advantage"
  - Line 354: "origines" is a misspelling of "origins"
  - Line 355: "origines" is a misspelling of "origins"
  - Line 370: "marrage" is a misspelling of "marriage"
- `docs/de/guide.md`
  - Line 7: "Konfiguration" is a misspelling of "Configuration"
  - Line 20: "Produktion" is a misspelling of "Production"
  - Line 90: "Produktion" is a misspelling of "Production"
  - Line 96: "Konfiguration" is a misspelling of "Configuration"
  - Line 124: "Konfiguration" is a misspelling of "Configuration"
  - Line 194: "Konfiguration" is a misspelling of "Configuration"
  - Line 237: "persistente" is a misspelling of "persistence"
  - Line 245: "Konfiguration" is a misspelling of "Configuration"
  - Line 255: "Konfiguration" is a misspelling of "Configuration"
  - Line 255: "Konfiguration" is a misspelling of "Configuration"
  - Line 256: "Konfiguration" is a misspelling of "Configuration"
  - Line 292: "Konfiguration" is a misspelling of "Configuration"
  - Line 305: "Konfiguration" is a misspelling of "Configuration"
  - Line 321: "Konfiguration" is a misspelling of "Configuration"
  - Line 321: "Konfiguration" is a misspelling of "Configuration"
  - Line 353: "Konfiguration" is a misspelling of "Configuration"
- `cmd/runner-manager/i18n/de.json`
  - Line 2: "Konfiguration" is a misspelling of "Configuration"
  - Line 8: "Konfiguration" is a misspelling of "Configuration"
  - Line 9: "Konfiguration" is a misspelling of "Configuration"
  - Line 26: "Konfiguration" is a misspelling of "Configuration"
  - Line 27: "Konfiguration" is a misspelling of "Configuration"
  - Line 70: "Konfiguration" is a misspelling of "Configuration"
  - Line 178: "Konfiguration" is a misspelling of "Configuration"

---

_Generated by [Go Report Card](https://github.com/soulteary/goreportcard-action)._
