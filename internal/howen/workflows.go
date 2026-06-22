package howen

import (
	"sync/atomic"

	"github.com/dfm/device-gateway/internal/core/flow"
)

// workflows.go holds the per-model visual mapping workflows (the "N8N-style"
// graphs). They are STRICTLY per model: a device uses its own model's active
// workflow, or — if that model has none — the built-in code→event tables
// (mapHowenEventCodes). There is no cross-model fallback.
//
// The active set is swapped atomically by ApplyWorkflows (called at startup and
// on every LISTEN/NOTIFY change), exactly like ApplyMappings.

var currentWorkflows atomic.Pointer[map[string]*flow.Graph]

func init() { currentWorkflows.Store(&map[string]*flow.Graph{}) }

// ApplyWorkflows installs the active per-model workflow set. A nil map clears it
// (every model reverts to the built-in tables).
func ApplyWorkflows(m map[string]*flow.Graph) {
	if m == nil {
		m = map[string]*flow.Graph{}
	}
	cp := make(map[string]*flow.Graph, len(m))
	for k, v := range m {
		cp[k] = v
	}
	currentWorkflows.Store(&cp)
}

// WorkflowModelCount reports how many models currently have an active workflow
// (used for a startup log line).
func WorkflowModelCount() int {
	if p := currentWorkflows.Load(); p != nil {
		return len(*p)
	}
	return 0
}

func workflowForModel(model string) *flow.Graph {
	p := currentWorkflows.Load()
	if p == nil {
		return nil
	}
	return (*p)[model]
}

// workflowInput projects an alarm payload into the field map the engine sees.
// These are the fields the editor's Input node exposes: eventCode, the raw
// detail object (tp, num, dt, st, …), the raw alarm object, and hasEndTime.
func workflowInput(ap *alarmPayload) map[string]any {
	in := map[string]any{
		"eventCode":  ap.EC,
		"hasEndTime": ap.EventEndUTC != nil,
	}
	if ap.Detail != nil {
		in["detail"] = ap.Detail
	}
	if ap.Alarm != nil {
		in["alarm"] = ap.Alarm
	}
	return in
}

// applyModelWorkflow runs the model's active workflow (if any) against the alarm
// and, when it matches, overrides the event list and any field overrides in the
// outgoing payload p. Returns true when a workflow produced the events.
func applyModelWorkflow(model string, ap *alarmPayload, p map[string]any) bool {
	g := workflowForModel(model)
	if g == nil {
		return false
	}
	res, err := g.Evaluate(workflowInput(ap))
	if err != nil || !res.Matched || len(res.Events) == 0 {
		return false
	}
	events := make([]any, len(res.Events))
	for i, e := range res.Events {
		events[i] = e
	}
	p["event"] = events
	for k, v := range res.Fields {
		p[k] = v
	}
	return true
}
