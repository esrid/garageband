package dbtest

import (
	"slices"
	"testing"
)

func TestReplaceEnvironment(t *testing.T) {
	got := replaceEnvironment(
		[]string{"PATH=/bin", "TEST_DATABASE_URL=old", "OTHER=value"},
		"TEST_DATABASE_URL",
		"postgres://new",
	)
	want := []string{"PATH=/bin", "OTHER=value", "TEST_DATABASE_URL=postgres://new"}
	if !slices.Equal(got, want) {
		t.Fatalf("environment = %v, want %v", got, want)
	}
}
