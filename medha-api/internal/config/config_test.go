package config

import (
	"strings"
	"testing"
)

func TestFromEnv_Defaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("SQLITE_PATH", "")
	c := FromEnv()
	if c.Port != 3111 {
		t.Errorf("Port = %d, want 3111", c.Port)
	}
	if c.ViewerPort != 3113 {
		t.Errorf("ViewerPort = %d, want 3113", c.ViewerPort)
	}
	if c.QueueBackend != "memory" {
		t.Errorf("QueueBackend = %q, want memory", c.QueueBackend)
	}
	if c.DecayRatePerDay != 0.95 {
		t.Errorf("DecayRatePerDay = %v, want 0.95", c.DecayRatePerDay)
	}
	if c.Neo4jEnabled {
		t.Error("Neo4jEnabled should default to false")
	}
	if err := c.Validate(); err != nil {
		t.Errorf("defaults should validate: %v", err)
	}
}

func TestValidate_PortRange(t *testing.T) {
	c := FromEnv()
	c.Port = 0
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "PORT") {
		t.Errorf("expected PORT error, got %v", err)
	}
}

func TestValidate_PortCollision(t *testing.T) {
	c := FromEnv()
	c.ViewerPort = c.Port
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Errorf("expected collision error, got %v", err)
	}
}

func TestValidate_QueueBackend(t *testing.T) {
	c := FromEnv()
	c.QueueBackend = "kafka"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "QUEUE_BACKEND") {
		t.Errorf("expected queue backend error, got %v", err)
	}
}

func TestValidate_RabbitMQRequired(t *testing.T) {
	c := FromEnv()
	c.QueueBackend = "rabbitmq"
	c.RabbitMQURL = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "RABBITMQ_URL") {
		t.Errorf("expected RABBITMQ_URL error, got %v", err)
	}
}

func TestValidate_DecayRate(t *testing.T) {
	c := FromEnv()
	c.DecayRatePerDay = 1.5
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "DECAY_RATE_PER_DAY") {
		t.Errorf("expected decay rate error, got %v", err)
	}
}

func TestGetBool(t *testing.T) {
	cases := map[string]bool{"true": true, "1": true, "yes": true, "false": false, "0": false}
	for in, want := range cases {
		t.Setenv("FEATURE_X", in)
		if got := getBool("FEATURE_X", !want); got != want {
			t.Errorf("getBool(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAddr(t *testing.T) {
	c := &Config{Port: 3111, ViewerPort: 3113}
	if c.Addr() != ":3111" {
		t.Errorf("Addr = %q, want :3111", c.Addr())
	}
	if c.ViewerAddr() != ":3113" {
		t.Errorf("ViewerAddr = %q, want :3113", c.ViewerAddr())
	}
}
