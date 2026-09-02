package main

import "testing"

func TestNormalizeDatabaseDSN(t *testing.T) {
	tests := map[string]string{
		"file:/data/airipress.db?cache=shared":       "file:/data/.meta/airipress.db?cache=shared",
		"file:airipress.db?cache=shared":             "file:.meta/airipress.db?cache=shared",
		"file:/srv/custom.db?cache=shared":           "file:/srv/custom.db?cache=shared",
		"postgres://db.example/airipress?sslmode=on": "postgres://db.example/airipress?sslmode=on",
	}
	for input, expected := range tests {
		if got := normalizeDatabaseDSN(input); got != expected {
			t.Errorf("normalizeDatabaseDSN(%q)=%q, want %q", input, got, expected)
		}
	}
}
