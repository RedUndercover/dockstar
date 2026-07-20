package main

import "testing"

func TestDisplayLabel(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"extend":        "Extend",
		"mirror":        "Mirror",
		"external-only": "External only",
		"internal-only": "Internal only",
		"custom":        "custom",
	}
	for input, want := range tests {
		input, want := input, want
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			if got := displayLabel(input); got != want {
				t.Fatalf("displayLabel(%q) = %q, want %q", input, got, want)
			}
		})
	}
}

func TestWakeActionRejectsUnknownAction(t *testing.T) {
	t.Parallel()
	if _, err := (&App{}).WakeAction("apply"); err == nil {
		t.Fatal("WakeAction accepted an unsupported action")
	}
}
