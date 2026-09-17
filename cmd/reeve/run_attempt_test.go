package main

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestFlagPositiveIntOrEnv(t *testing.T) {
	tests := []struct {
		name      string
		flagValue *string
		envValue  string
		want      int
		wantErr   bool
	}{
		{name: "unset"},
		{name: "environment", envValue: "2", want: 2},
		{name: "flag overrides environment", flagValue: stringPointer("3"), envValue: "2", want: 3},
		{name: "malformed environment", envValue: "second", wantErr: true},
		{name: "zero environment", envValue: "0", wantErr: true},
		{name: "negative environment", envValue: "-1", wantErr: true},
		{name: "empty explicit flag", flagValue: stringPointer(""), envValue: "2", wantErr: true},
		{name: "malformed flag", flagValue: stringPointer("second"), envValue: "2", wantErr: true},
		{name: "zero flag", flagValue: stringPointer("0"), envValue: "2", wantErr: true},
		{name: "negative flag", flagValue: stringPointer("-1"), envValue: "2", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("REEVE_TEST_RUN_ATTEMPT", tt.envValue)
			cmd := &cobra.Command{Use: "test"}
			cmd.Flags().String("run-attempt", "", "")
			if tt.flagValue != nil {
				if err := cmd.Flags().Set("run-attempt", *tt.flagValue); err != nil {
					t.Fatal(err)
				}
			}
			got, err := flagPositiveIntOrEnv(cmd, "run-attempt", "REEVE_TEST_RUN_ATTEMPT")
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr = %t", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("attempt = %d, want %d", got, tt.want)
			}
		})
	}
}

func stringPointer(value string) *string { return &value }
