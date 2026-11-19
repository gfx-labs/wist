package daemon

import (
	"context"
	"encoding/json/jsontext"

	acp "github.com/gfx-labs/go-acp"
)

// MethodShutdown stops the daemon and its agent.
const MethodShutdown = "_wist/shutdown"

// Request sends one extension request to the daemon at socket without
// starting a daemon, and returns its raw result.
func Request(ctx context.Context, socket, method string, params any) (jsontext.Value, error) {
	c, _, err := Dial(ctx, socket, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	conn := acp.NewClientSideConnection(nopClient{}, c, c, nil)
	go func() { _ = conn.Serve(ctx) }()
	return conn.SendExtensionRequest(ctx, method, params)
}

// nopClient is an ACP client that advertises nothing and answers nothing.
type nopClient struct{}

var _ acp.Client = nopClient{}

func (nopClient) SessionUpdate(context.Context, acp.SessionNotification) error { return nil }

func (nopClient) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, acp.NewMethodNotFound(acp.ClientMethodSessionRequestPermission)
}

func (nopClient) CompleteElicitation(context.Context, acp.CompleteElicitationNotification) error {
	return nil
}

func (nopClient) CreateElicitation(context.Context, acp.CreateElicitationRequest) (acp.CreateElicitationResponse, error) {
	return acp.CreateElicitationResponse{}, acp.NewMethodNotFound(acp.ClientMethodElicitationCreate)
}

func (nopClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsReadTextFile)
}

func (nopClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsWriteTextFile)
}

func (nopClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalCreate)
}

func (nopClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalKill)
}

func (nopClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalOutput)
}

func (nopClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalRelease)
}

func (nopClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalWaitForExit)
}
