package probe

import (
	"time"

	"steel/internal/inventory"
	"steel/internal/runner/ping"
)

// pingProbe is the package-level indirection behind the default
// ProbeFn. Keeping it in its own file isolates the runner/ping
// import — the rest of the package can be tested without ever
// importing runner/ping, by overriding ProbeFn directly.
func pingProbe(h *inventory.Host, timeout time.Duration) (time.Duration, error) {
	return ping.Probe(h, timeout)
}
