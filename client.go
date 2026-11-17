package main

import (
	"context"

	acp "github.com/gfx-labs/go-acp"
)

// client is the ACP client towa talks to. Every callback runs on a
// connection goroutine and hands its work to the UI goroutine.
type client struct {
	ui *UI
}

var _ acp.Client = (*client)(nil)

func (c *client) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	c.ui.post(func(u *UI) { u.applyUpdate(n) })
	return nil
}

func (c *client) RequestPermission(ctx context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	answer := make(chan acp.RequestPermissionOutcome, 1)
	c.ui.post(func(u *UI) { u.askPermission(p, answer) })
	select {
	case out := <-answer:
		return acp.RequestPermissionResponse{Outcome: out}, nil
	case <-ctx.Done():
		c.ui.post(func(u *UI) { u.dropPermission(answer) })
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
	}
}

// The capabilities below are not advertised in initialize, so a
// conforming agent never calls them.

func (c *client) CompleteElicitation(context.Context, acp.CompleteElicitationNotification) error {
	return nil
}

func (c *client) CreateElicitation(context.Context, acp.CreateElicitationRequest) (acp.CreateElicitationResponse, error) {
	return acp.CreateElicitationResponse{}, acp.NewMethodNotFound(acp.ClientMethodElicitationCreate)
}

func (c *client) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsReadTextFile)
}

func (c *client) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsWriteTextFile)
}

func (c *client) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalCreate)
}

func (c *client) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalKill)
}

func (c *client) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalOutput)
}

func (c *client) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalRelease)
}

func (c *client) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalWaitForExit)
}
