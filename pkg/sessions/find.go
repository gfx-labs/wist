package sessions

import (
	"context"
	"strings"

	acp "github.com/gfx-labs/go-acp"
)

// Find returns the sessions ref names: the session whose id is ref, else
// those whose title equals ref ignoring case, else those whose title
// contains ref ignoring case. The order of list is kept.
func Find(list []acp.SessionInfo, ref string) []acp.SessionInfo {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	for _, s := range list {
		if string(s.SessionId) == ref {
			return []acp.SessionInfo{s}
		}
	}
	q := strings.ToLower(ref)
	var exact, part []acp.SessionInfo
	for _, s := range list {
		switch t := strings.ToLower(strings.TrimSpace(deref(s.Title))); {
		case t == "":
		case t == q:
			exact = append(exact, s)
		case strings.Contains(t, q):
			part = append(part, s)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return part
}

// ListAll fetches every page of a session listing.
func ListAll(ctx context.Context, list func(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error), req acp.ListSessionsRequest) ([]acp.SessionInfo, error) {
	var out []acp.SessionInfo
	for {
		resp, err := list(ctx, req)
		if err != nil {
			return nil, err
		}
		out = append(out, resp.Sessions...)
		if resp.NextCursor == nil {
			return out, nil
		}
		req.Cursor = resp.NextCursor
	}
}
