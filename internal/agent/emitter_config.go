package agent

import (
	"os"
	"strconv"
	"time"
)

// Env var names for the driver-side emitter. Both are read by
// EmitterFromEnv; unset URL means NopEmitter (no fault).
const (
	EnvHatchURL       = "HEARTH_AGENT_HATCH_URL"
	EnvHatchTimeoutMS = "HEARTH_AGENT_HATCH_TIMEOUT_MS"
)

// EmitterFromEnv builds the Emitter the hearth binary uses at boot.
// Returns NopEmitter{} when HEARTH_AGENT_HATCH_URL is unset — the
// operator has not opted the deployment into agent-projection yet.
func EmitterFromEnv() Emitter {
	url := os.Getenv(EnvHatchURL)
	if url == "" {
		return NopEmitter{}
	}
	timeout := DefaultTimeout
	if raw := os.Getenv(EnvHatchTimeoutMS); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			timeout = time.Duration(n) * time.Millisecond
		}
	}
	return NewHTTPEmitter(url, timeout, nil)
}
