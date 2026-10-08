package main

import (
	"context"
	"testing"

	"github.com/urfave/cli/v3"
)

// resolvePromoteSource keeps `--deployment` working while `--from` becomes the
// documented spelling, so both need coverage.
func TestResolvePromoteSource(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantSource string
		wantErr    bool
	}{
		{name: "from", args: []string{"promote", "--from", "dev"}, wantSource: "dev"},
		{name: "deployment still works", args: []string{"promote", "--deployment", "staging"}, wantSource: "staging"},
		{name: "short alias still works", args: []string{"promote", "-d", "staging"}, wantSource: "staging"},
		{name: "both, same value", args: []string{"promote", "--from", "dev", "--deployment", "dev"}, wantSource: "dev"},
		{name: "both, conflicting", args: []string{"promote", "--from", "dev", "--deployment", "qa"}, wantErr: true},
		{name: "neither", args: []string{"promote"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			var gotErr error
			cmd := &cli.Command{
				Name: "promote",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "from"},
					&cli.StringFlag{Name: "to"},
					&cli.StringFlag{Name: "deployment", Aliases: []string{"d"}},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					got, gotErr = resolvePromoteSource(c)
					return nil
				},
			}
			if err := cmd.Run(context.Background(), tt.args); err != nil {
				t.Fatalf("run: %v", err)
			}
			if tt.wantErr {
				if gotErr == nil {
					t.Fatalf("expected an error, got source %q", got)
				}
				return
			}
			if gotErr != nil {
				t.Fatalf("unexpected error: %v", gotErr)
			}
			if got != tt.wantSource {
				t.Fatalf("source = %q, want %q", got, tt.wantSource)
			}
		})
	}
}

// The destination defaults to production, which the API represents as "".
func TestPromoteDestinationDefaultsToProduction(t *testing.T) {
	var dst string
	cmd := &cli.Command{
		Name:  "promote",
		Flags: []cli.Flag{&cli.StringFlag{Name: "to"}},
		Action: func(ctx context.Context, c *cli.Command) error {
			dst = c.String("to")
			return nil
		},
	}
	if err := cmd.Run(context.Background(), []string{"promote"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if dst != "" {
		t.Fatalf("default destination = %q, want empty (production)", dst)
	}
}
