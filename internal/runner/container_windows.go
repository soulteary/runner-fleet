package runner

import (
	"context"
	"fmt"

	"github.com/soulteary/runner-fleet/internal/config"
)

// Container mode is rejected by config.Validate on Windows. These entry points
// keep the shared HTTP API buildable and fail explicitly if called directly.
func unsupportedContainers() error {
	return fmt.Errorf("Windows container mode is not supported; use native runner services")
}
func ContainerName(name string) string { return config.NormalizedContainerName(name) }
func ManagerDockerHostIsDind() bool    { return false }
func StartRunnerContainer(context.Context, *config.Config, string, string) error {
	return unsupportedContainers()
}
func RecreateRunnerContainer(context.Context, *config.Config, string, string) error {
	return unsupportedContainers()
}
func StopRunnerContainer(context.Context, string) error   { return unsupportedContainers() }
func RemoveRunnerContainer(context.Context, string) error { return unsupportedContainers() }
func ContainerRunnerStatus(context.Context, *config.Config, string, string) (bool, Status, string, error) {
	return false, StatusUnknown, "", unsupportedContainers()
}
func ContainerState(context.Context, string) (bool, string, error) {
	return false, "", unsupportedContainers()
}
