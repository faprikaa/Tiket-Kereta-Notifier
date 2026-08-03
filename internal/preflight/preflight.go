// Package preflight validates external runtime dependencies before the bot
// starts provider schedulers.
package preflight

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"tiket-kereta-notifier/internal/config"
)

// Check reports every runtime dependency missing for the enabled features.
// It never installs or changes software on the host.
func Check(cfg *config.Config) error {
	var missing []string
	requiredProviders := make(map[string]bool)
	for _, train := range cfg.FlatTrains {
		requiredProviders[strings.ToLower(train.ProviderName)] = true
	}

	if requiredProviders["bookingkai"] && !chromiumAvailable(cfg.Browser.ChromiumPath) {
		missing = append(missing, "chromium (run ./scripts/setup-ubuntu.sh or set browser.chromium_path)")
	}
	if requiredProviders["tiketcom"] && !commandAvailable("curl_chrome110") {
		missing = append(missing, "curl_chrome110 (run ./scripts/setup-ubuntu.sh)")
	}
	if cfg.Webhook.Enabled && !commandAvailable("cloudflared") {
		missing = append(missing, "cloudflared (run ./scripts/setup-ubuntu.sh)")
	}

	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("missing runtime dependencies:\n- %s", strings.Join(missing, "\n- "))
}

func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func chromiumAvailable(configuredPath string) bool {
	if configuredPath != "" {
		info, err := os.Stat(configuredPath)
		return err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0
	}

	for _, name := range []string{"chromium-browser", "chromium", "google-chrome-stable", "google-chrome"} {
		if commandAvailable(name) {
			return true
		}
	}
	return false
}
