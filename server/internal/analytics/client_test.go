package analytics

import "testing"

func TestNewFromEnv_AlwaysNoop(t *testing.T) {
	c := NewFromEnv()
	if _, ok := c.(NoopClient); !ok {
		t.Fatalf("NewFromEnv() = %T, want NoopClient", c)
	}
	c.Capture(Event{Name: "signup", DistinctID: "u1"})
	c.Close()
}

func TestEnvironmentFromEnv_DefaultDev(t *testing.T) {
	t.Setenv("ANALYTICS_ENVIRONMENT", "")
	t.Setenv("APP_ENV", "")
	if got := EnvironmentFromEnv(); got != "dev" {
		t.Fatalf("EnvironmentFromEnv() = %q, want dev", got)
	}
}
