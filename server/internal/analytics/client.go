// Package analytics provides a no-op product-event client.
// Capture call sites remain so handlers keep a uniform shape; events are dropped.
package analytics

import (
	"os"
	"strings"
	"time"
)

// Event is a single analytics capture. Fields mirror the historical event
// shape so existing call sites compile without change.
type Event struct {
	Name        string
	DistinctID  string
	WorkspaceID string
	Properties  map[string]any
	SetOnce     map[string]any
	Set         map[string]any
	Timestamp   time.Time
}

// Client is the narrow surface the rest of the codebase depends on.
type Client interface {
	Capture(e Event)
	Close()
}

// NewFromEnv always returns a no-op client. Product telemetry is disabled.
func NewFromEnv() Client {
	return NoopClient{}
}

func EnvironmentFromEnv() string {
	if v := normalizeEnvironment(os.Getenv("ANALYTICS_ENVIRONMENT")); v != "" {
		return v
	}
	if v := normalizeEnvironment(os.Getenv("APP_ENV")); v != "" {
		return v
	}
	return "dev"
}

func normalizeEnvironment(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "production", "prod":
		return "production"
	case "staging", "stage":
		return "staging"
	case "development", "dev", "test", "local":
		return "dev"
	default:
		return ""
	}
}

// NoopClient silently drops all events.
type NoopClient struct{}

func (NoopClient) Capture(Event) {}
func (NoopClient) Close()        {}
