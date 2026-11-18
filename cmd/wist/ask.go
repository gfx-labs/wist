package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gfx-labs/wist/pkg/ask"
	"github.com/gfx-labs/wist/pkg/config"
	"golang.org/x/term"
)

// runAsk is `wist ask`; it returns the exit status.
func runAsk(args []string, cfg config.Config) int {
	piped := !term.IsTerminal(int(os.Stdin.Fd()))
	o, err := ask.ParseFlags(args, cfg, os.Stdin, piped, os.Stderr)
	if errors.Is(err, ask.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "wist ask:", err)
		return ask.ExitCode("", err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		s := <-sigs
		cancel(ask.Interrupted{Signal: s})
		<-sigs // a second signal exits at once
		os.Exit(ask.Interrupted{Signal: s}.Code())
	}()

	res, err := ask.Run(ctx, o)
	var intr ask.Interrupted
	if errors.As(err, &intr) {
		fmt.Fprintln(os.Stderr, "wist ask: interrupted")
		return intr.Code()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "wist ask:", err)
	}
	if o.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(res)
	}
	return res.ExitCode
}
