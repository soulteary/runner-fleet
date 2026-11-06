package runner

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/soulteary/runner-fleet/internal/config"
)

type CheckLevel string

const (
	CheckOK            CheckLevel = "ok"
	CheckWarn          CheckLevel = "warn"
	CheckError         CheckLevel = "error"
	PreflightLogMarker            = "[preflight"
)

type CheckResult struct {
	Name    string
	Level   CheckLevel
	Message string
	Hint    string
}

func Preflight(ctx context.Context, cfg *config.Config) []CheckResult {
	if cfg == nil {
		return []CheckResult{{Name: "config", Level: CheckError, Message: "configuration is empty"}}
	}
	if err := ctx.Err(); err != nil {
		return []CheckResult{{Name: "Windows services", Level: CheckWarn, Message: err.Error()}}
	}
	if _, err := os.Stat(cfg.Runners.BasePath); err != nil {
		return []CheckResult{{Name: "Windows runner directory", Level: CheckWarn, Message: err.Error(), Hint: "The directory will be created when adding the first runner"}}
	}
	return []CheckResult{{Name: "Windows services", Level: CheckOK, Message: "Runner lifecycle uses Windows SCM; Manager uses LocalSystem and runners use NetworkService; build tools must be accessible to NetworkService"}}
}
func LogPreflight(results []CheckResult) {
	for _, r := range results {
		log.Print(fmt.Sprintf("%s %s] %s: %s %s", PreflightLogMarker, r.Level, r.Name, r.Message, r.Hint))
	}
}
