# Go Report Card

**Grade: A+** (97.9%)

| Metric | Value |
| ------ | ----- |
| Files | 112 |
| Issues | 28 |

## Checks

| Check | Score |
| ----- | ----- |
| go_vet | 100% |
| gofmt | 100% |
| ineffassign | 100% |
| gocyclo | 82% |
| license | 100% |
| misspell | 92% |

## Issues

### gocyclo

- `internal/handler/handler.go`
  - Line 377: cyclomatic complexity 27 for function AddRunner
  - Line 701: cyclomatic complexity 21 for function UpdateRunner
  - Line 102: cyclomatic complexity 16 for function runRegistrationJob
- `internal/handler/container_behavior_test.go`
  - Line 30: cyclomatic complexity 25 for function TestContainerAPIUsesAgentStatusAndPreservesProbeFailures
- `internal/runner/container_failure_paths_test.go`
  - Line 16: cyclomatic complexity 24 for function TestContainerStartStopsAtDockerFailureBoundaries
- `internal/runner/drift.go`
  - Line 201: cyclomatic complexity 19 for function (containerSpec).driftReason
- `internal/runner/githubstatus_test.go`
  - Line 12: cyclomatic complexity 17 for function TestGitHubStatus_RoundTripsThreeStates
- `internal/secrets/guardrail_test.go`
  - Line 88: cyclomatic complexity 16 for function TestNoManagerSecretsUnderRunnerDirs
- `internal/handler/remove_failure_test.go`
  - Line 153: cyclomatic complexity 16 for function TestRemoveRunnerReportsConfigCommitFailureAfterCleanup
- `internal/docsconsistency/envvars_test.go`
  - Line 43: cyclomatic complexity 20 for function TestEnvVars_EveryVariableTheCodeReadsIsDocumented
- `internal/handler/process_behavior_test.go`
  - Line 18: cyclomatic complexity 18 for function TestProcessAPIReusesRunningRunnerAndDeliversStop
- `internal/docsconsistency/apikeys_test.go`
  - Line 29: cyclomatic complexity 23 for function TestAPIKeys_EveryKeyTheHandlerUsesExistsInAllLanguages
- `cmd/runner-manager/entrypoint_test.go`
  - Line 33: cyclomatic complexity 23 for function TestManagerEntrypointServesAndShutsDown
- `cmd/runner-agent/entrypoint_test.go`
  - Line 26: cyclomatic complexity 22 for function TestAgentEntrypointLifecycle
- `internal/handler/precheck.go`
  - Line 92: cyclomatic complexity 17 for function collectRunnerConflicts
- `internal/config/config.go`
  - Line 404: cyclomatic complexity 30 for function Validate
  - Line 195: cyclomatic complexity 19 for function (ResourceLimits).Validate
  - Line 343: cyclomatic complexity 17 for function Load
- `internal/handler/api_behavior_test.go`
  - Line 131: cyclomatic complexity 24 for function TestAddRegistrationQueuePreservesAcceptedState
  - Line 85: cyclomatic complexity 16 for function TestViewsUseCurrentRunnerStateAndTranslationFallback
- `internal/runner/container.go`
  - Line 262: cyclomatic complexity 21 for function startRunnerContainer
- `internal/handler/precheck_behavior_test.go`
  - Line 45: cyclomatic complexity 20 for function TestPrecheckDockerFailuresAndConflictsAreExplicit
- `internal/runnerproc/runnerproc_test.go`
  - Line 180: cyclomatic complexity 17 for function TestFind_RealProcesses
- `internal/runner/failure_behavior_test.go`
  - Line 50: cyclomatic complexity 17 for function TestPersistedStatusRejectsCorruptRecordsAndKeepsValidDetails
- `internal/secrets/storage_failure_test.go`
  - Line 12: cyclomatic complexity 16 for function TestMigrationRetryAfterDestinationCommitAndSourceRemovalFailure

### misspell

- `docs/de/guide.md`
  - Line 7: "Konfiguration" is a misspelling of "Configuration"
  - Line 20: "Produktion" is a misspelling of "Production"
  - Line 90: "Produktion" is a misspelling of "Production"
  - Line 96: "Konfiguration" is a misspelling of "Configuration"
  - Line 124: "Konfiguration" is a misspelling of "Configuration"
  - Line 194: "Konfiguration" is a misspelling of "Configuration"
  - Line 223: "Konfiguration" is a misspelling of "Configuration"
  - Line 223: "Konfiguration" is a misspelling of "Configuration"
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
- `docs/fr/README.md`
  - Line 9: "conteneurs" is a misspelling of "contenders"
  - Line 10: "Superposer" is a misspelling of "Superpowers"
- `docs/fr/development.md`
  - Line 7: "contributeurs" is a misspelling of "contributes"
  - Line 63: "existant" is a misspelling of "existent"
  - Line 66: "marrage" is a misspelling of "marriage"
  - Line 115: "marrage" is a misspelling of "marriage"
  - Line 117: "conflit" is a misspelling of "conflict"
  - Line 119: "marrage" is a misspelling of "marriage"
  - Line 142: "constituer" is a misspelling of "constitute"
  - Line 154: "origines" is a misspelling of "origins"
  - Line 156: "origines" is a misspelling of "origins"
  - Line 162: "Exemple" is a misspelling of "Example"
  - Line 194: "marrage" is a misspelling of "marriage"
  - Line 201: "transforme" is a misspelling of "transfer"
  - Line 216: "existant" is a misspelling of "existent"
  - Line 246: "pointeur" is a misspelling of "pointer"
  - Line 246: "pointeur" is a misspelling of "pointer"
  - Line 262: "exemple" is a misspelling of "example"
  - Line 263: "construits" is a misspelling of "constructs"
  - Line 298: "pointeur" is a misspelling of "pointer"
  - Line 348: "marrage" is a misspelling of "marriage"
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
  - Line 237: "marrage" is a misspelling of "marriage"
  - Line 237: "persistant" is a misspelling of "persistent"
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
- `cmd/runner-manager/i18n/de.json`
  - Line 2: "Konfiguration" is a misspelling of "Configuration"
  - Line 8: "Konfiguration" is a misspelling of "Configuration"
  - Line 9: "Konfiguration" is a misspelling of "Configuration"
  - Line 26: "Konfiguration" is a misspelling of "Configuration"
  - Line 27: "Konfiguration" is a misspelling of "Configuration"
  - Line 70: "Konfiguration" is a misspelling of "Configuration"
  - Line 178: "Konfiguration" is a misspelling of "Configuration"
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
  - Line 109: "Konfiguration" is a misspelling of "Configuration"
  - Line 117: "Konfiguration" is a misspelling of "Configuration"
  - Line 118: "Konfiguration" is a misspelling of "Configuration"
  - Line 272: "Produktion" is a misspelling of "Production"

---

_Generated by [Go Report Card](https://github.com/soulteary/goreportcard-action)._
