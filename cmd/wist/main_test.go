package main

import (
	"context"
	"io"
	"testing"

	"github.com/gfx-labs/wist/pkg/config"
	"github.com/gfx-labs/wist/pkg/tui"
	"github.com/urfave/cli/v3"
)

// TestRootArgs checks the root command keeps the old flag semantics:
// flags before --, everything after -- goes to the agent.
func TestRootArgs(t *testing.T) {
	cfg := config.Config{TowaArgs: []string{"--default"}}
	cmd := rootCommand(cfg)
	var got tui.Options
	var extraSeen string
	cmd.Writer, cmd.ErrWriter = io.Discard, io.Discard
	cmd.Action = func(_ context.Context, c *cli.Command) error {
		got = tui.Options{Towa: c.String("towa"), Resume: c.String("resume"), Prompt: c.String("p")}
		extraSeen = c.String("towa-args")
		finishOptions(&got, extraSeen, c.Args().Slice(), cfg)
		return nil
	}
	if err := cmd.Run(context.Background(), []string{"wist", "--resume", "abc", "-p", "hi", "--", "--models=x", "-v"}); err != nil {
		t.Fatal(err)
	}
	if got.Resume != "abc" || got.Prompt != "hi" || len(got.Args) != 2 || got.Args[0] != "--models=x" || got.Args[1] != "-v" {
		t.Fatalf("got %+v", got)
	}
	cmd = rootCommand(cfg)
	cmd.Action = func(_ context.Context, c *cli.Command) error {
		got = tui.Options{}
		finishOptions(&got, "", c.Args().Slice(), cfg)
		return nil
	}
	if err := cmd.Run(context.Background(), []string{"wist"}); err != nil || len(got.Args) != 1 || got.Args[0] != "--default" {
		t.Fatalf("default towa args: %+v %v", got, err)
	}
}

func TestUsageErrorExit2(t *testing.T) {
	cmd := rootCommand(config.Config{})
	cmd.Writer, cmd.ErrWriter = io.Discard, io.Discard
	err := cmd.Run(context.Background(), []string{"wist", "ask", "--bogus", "x"})
	ec, ok := err.(cli.ExitCoder)
	if !ok || ec.ExitCode() != 2 {
		t.Fatalf("err = %v", err)
	}
}
