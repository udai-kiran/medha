// Package testutil provides helpers for PostgreSQL integration tests.
// Tests that call OpenStore will be skipped unless POSTGRES_TEST_HOST is set.
package testutil

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/udai-kiran/medha/internal/state"
)

// OpenStore opens a *state.Store using POSTGRES_TEST_* env vars.
// The test is skipped automatically if POSTGRES_TEST_HOST is not set.
func OpenStore(t *testing.T) *state.Store {
	t.Helper()
	host := os.Getenv("POSTGRES_TEST_HOST")
	if host == "" {
		t.Skip("POSTGRES_TEST_HOST not set; skipping PostgreSQL integration test")
	}
	port, _ := strconv.Atoi(os.Getenv("POSTGRES_TEST_PORT"))
	if port == 0 {
		port = 5432
	}
	user := os.Getenv("POSTGRES_TEST_USER")
	if user == "" {
		user = "medha"
	}
	password := os.Getenv("POSTGRES_TEST_PASSWORD")
	if password == "" {
		password = "medha-password"
	}
	database := os.Getenv("POSTGRES_TEST_DB")
	if database == "" {
		database = "medha_test"
	}
	s, err := state.Open(context.Background(), state.Options{
		Host:     host,
		Port:     port,
		User:     user,
		Password: password,
		Database: database,
		SSLMode:  "disable",
	})
	if err != nil {
		t.Fatalf("testutil.OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
