package scripting

import (
	"context"
	"time"

	"github.com/cuppojoe/feather/internal/http"
	"github.com/cuppojoe/feather/internal/models"
	"github.com/cuppojoe/feather/internal/openapi"
	"github.com/cuppojoe/feather/internal/overlay"
)

// ChainScripts is a typed snippet bundle. The host resolves these once per
// request via Resolve() and then runs them with RunChain().
type ChainScripts struct {
	Profile string
	Tag     string // the tag-source label this snippet came from (display only)
	TagJS   string
	OpJS    string
}

// Resolve gathers the scripts for one phase, applied to one endpoint, in
// execution order. For pre-request scripts that's profile → tag → operation;
// for post-request it's operation → tag → profile (the unwind).
func Resolve(ov *overlay.Overlay, ep *openapi.Endpoint, profile string, phase Phase) []ScriptSnippet {
	if ov == nil || ep == nil {
		return nil
	}
	prof := ov.ProfileScripts()
	opp := ov.OperationScripts(ep.Method, ep.Path)

	tag := ""
	if len(ep.Tags) > 0 {
		tag = ep.Tags[0]
	}
	tagS := ov.TagScripts(tag)

	pre := []ScriptSnippet{
		{Scope: ScopeProfile, Profile: profile, Tag: tag, Code: prof.Pre},
		{Scope: ScopeTag, Profile: profile, Tag: tag, Code: tagS.Pre},
		{Scope: ScopeOperation, Profile: profile, Tag: tag, Code: opp.Pre},
	}
	post := []ScriptSnippet{
		{Scope: ScopeOperation, Profile: profile, Tag: tag, Code: opp.Post},
		{Scope: ScopeTag, Profile: profile, Tag: tag, Code: tagS.Post},
		{Scope: ScopeProfile, Profile: profile, Tag: tag, Code: prof.Post},
	}
	if phase == PhasePre {
		return filterNonEmpty(pre)
	}
	return filterNonEmpty(post)
}

// ScriptSnippet is a single resolved script ready to run.
type ScriptSnippet struct {
	Scope   Scope
	Profile string
	Tag     string
	Code    string
}

func filterNonEmpty(ss []ScriptSnippet) []ScriptSnippet {
	out := ss[:0]
	for _, s := range ss {
		if s.Code != "" {
			out = append(out, s)
		}
	}
	return out
}

// RunChain executes a resolved chain. Each snippet sees the same Env, which
// means request/response/context mutations propagate to the next snippet in
// the chain. Returns the aggregated results in run order. The first aborted
// pre-script stops the chain (subsequent results aren't appended).
func RunChain(
	ctx context.Context,
	snippets []ScriptSnippet,
	phase Phase,
	endpoint *openapi.Endpoint,
	req *http.Request,
	resp *http.Response,
	cctx *models.Context,
	timeoutMs int,
) []Result {
	if len(snippets) == 0 {
		return nil
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond
	results := make([]Result, 0, len(snippets))
	for _, s := range snippets {
		env := &Env{
			Phase:    phase,
			Scope:    s.Scope,
			Profile:  s.Profile,
			Tag:      s.Tag,
			Endpoint: endpoint,
			Request:  req,
			Response: resp,
			Context:  cctx,
		}
		r := Run(ctx, s.Code, env, timeout)
		results = append(results, r)
		if phase == PhasePre && r.Aborted {
			break
		}
	}
	return results
}

// AnyAborted reports whether any pre-script aborted the chain.
func AnyAborted(rs []Result) (bool, string) {
	for _, r := range rs {
		if r.Aborted {
			return true, r.Reason
		}
	}
	return false, ""
}

// AllLogs flattens logs from a list of results in the order they ran.
func AllLogs(rs []Result) []LogEntry {
	n := 0
	for _, r := range rs {
		n += len(r.Logs)
	}
	out := make([]LogEntry, 0, n)
	for _, r := range rs {
		out = append(out, r.Logs...)
	}
	return out
}

// AnyError returns the first non-abort Err encountered, if any.
func AnyError(rs []Result) error {
	for _, r := range rs {
		if r.Err != nil {
			return r.Err
		}
	}
	return nil
}
