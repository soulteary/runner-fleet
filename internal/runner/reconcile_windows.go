package runner

import (
	"context"
	"log"
	"time"

	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/windowsrunner"
)

// ReconcileWindows reloads intent while holding the same per-installation lock
// as API mutations. A stale periodic snapshot cannot undo a user's Stop.
func ReconcileWindows(ctx context.Context, configPath string) {
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Printf("Windows reconciliation: %v", err)
		return
	}
	for _, item := range cfg.Runners.Items {
		func() {
			dir := item.InstallPath(cfg.Runners.BasePath)
			unlock := windowsrunner.Lock(dir)
			defer unlock()
			current, err := config.Load(configPath)
			if err != nil {
				return
			}
			fresh, ok := current.FindItem(item.Name)
			if !ok || fresh.InstallPath(current.Runners.BasePath) != dir || diskStatus(dir) != StatusInstalled {
				return
			}
			// An unknown service is not assumed dead. Repair is an explicit Start action.
			state, err := windowsrunner.Default.Status(dir)
			if err != nil || state == windowsrunner.Pending {
				return
			}
			opCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			if err := windowsrunner.Default.SetRunning(opCtx, dir, fresh.WantsRunning()); err != nil {
				log.Printf("Windows runner %s reconciliation failed: %v", item.Name, err)
			}
		}()
	}
}
