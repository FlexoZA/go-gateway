// Package flow is a small, deterministic node-graph interpreter — the engine
// behind the admin panel's visual ("N8N-style") device-mapping editor.
//
// A workflow is a graph of nodes connected by edges. Evaluation starts at the
// single input node and walks a SINGLE path: at each decision node (switch /
// condition) exactly one outgoing edge is chosen, and set nodes along the way
// accumulate output events/fields until an output node (or a dead end) is hit.
// This single-path model keeps evaluation predictable and cheap (no merges, no
// parallelism) and maps cleanly onto a visual canvas.
//
// The engine is protocol-agnostic: it operates on a generic map[string]any
// payload and produces events + field overrides. Protocol plugins decide what to
// feed in and how to apply the result.
package flow

import (
	"fmt"
	"strconv"
	"strings"
)

// Node types understood by the engine. Unknown types are treated as pass-through.
const (
	NodeInput     = "input"     // entry point (exactly one); passes the payload on
	NodeSwitch    = "switch"    // branch on a field's value (edges labelled case:<v> / default)
	NodeCondition = "condition" // field <op> value → edges labelled true / false
	NodeSetEvent  = "setEvent"  // append an event code to the output
	NodeSetField  = "setField"  // set an output field override
	NodeOutput    = "output"    // terminal; marks a matched result
)

// Graph is a workflow: nodes plus the edges between them. It is the JSON shape
// the React Flow editor saves and the gateway loads.
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Node is one graph node. Position is editor-only and ignored by the engine.
type Node struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Data     map[string]any `json:"data,omitempty"`
	Position map[string]any `json:"position,omitempty"`
}

// Edge connects one node's output to another's input. SourceHandle labels which
// branch the edge leaves a decision node by (e.g. "case:34", "default", "true").
type Edge struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	Target       string `json:"target"`
	SourceHandle string `json:"sourceHandle,omitempty"`
}

// Result is the outcome of evaluating a graph against a payload.
type Result struct {
	// Matched is true when traversal reached an output node.
	Matched bool `json:"matched"`
	// Events are the accumulated event codes (in path order, de-duplicated).
	Events []string `json:"events"`
	// Fields are accumulated output field overrides.
	Fields map[string]any `json:"fields"`
	// Trace is the ordered list of visited node IDs (for the editor's test view).
	Trace []string `json:"trace"`
}

// Validate checks structural invariants the engine relies on. It is cheap and is
// used both at load time and by the dry-run API so the editor can surface
// problems before saving.
func (g *Graph) Validate() error {
	inputs := 0
	ids := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		if n.ID == "" {
			return fmt.Errorf("node with empty id")
		}
		if ids[n.ID] {
			return fmt.Errorf("duplicate node id %q", n.ID)
		}
		ids[n.ID] = true
		if n.Type == NodeInput {
			inputs++
		}
	}
	if inputs == 0 {
		return fmt.Errorf("graph has no input node")
	}
	if inputs > 1 {
		return fmt.Errorf("graph has %d input nodes (expected 1)", inputs)
	}
	for _, e := range g.Edges {
		if !ids[e.Source] {
			return fmt.Errorf("edge %q has unknown source %q", e.ID, e.Source)
		}
		if !ids[e.Target] {
			return fmt.Errorf("edge %q has unknown target %q", e.ID, e.Target)
		}
	}
	return nil
}

// Evaluate walks the graph against payload and returns the accumulated result.
// It never errors on data; it returns Matched=false when no path reaches an
// output. A structural problem (no input node, cycle) returns an error.
func (g *Graph) Evaluate(payload map[string]any) (Result, error) {
	nodes := make(map[string]Node, len(g.Nodes))
	var inputID string
	for _, n := range g.Nodes {
		nodes[n.ID] = n
		if n.Type == NodeInput {
			inputID = n.ID
		}
	}
	if inputID == "" {
		return Result{}, fmt.Errorf("graph has no input node")
	}

	out := make(map[string][]Edge)
	for _, e := range g.Edges {
		out[e.Source] = append(out[e.Source], e)
	}

	res := Result{Fields: map[string]any{}}
	seen := map[string]bool{}
	cur := inputID
	// Cap steps at node count to guarantee termination even on a malformed cycle.
	for steps := 0; steps <= len(g.Nodes); steps++ {
		if cur == "" {
			return res, nil
		}
		if seen[cur] {
			return res, fmt.Errorf("cycle detected at node %q", cur)
		}
		seen[cur] = true
		n := nodes[cur]
		res.Trace = append(res.Trace, cur)

		switch n.Type {
		case NodeOutput:
			res.Matched = true
			return res, nil
		case NodeSetEvent:
			res.addEvent(dataStr(n.Data, "event"))
		case NodeSetField:
			if k := dataStr(n.Data, "key"); k != "" {
				res.Fields[k] = n.Data["value"]
			}
		}

		cur = nextNode(n, out[cur], payload)
	}
	return res, fmt.Errorf("graph did not terminate (possible cycle)")
}

// nextNode chooses the single outgoing edge to follow from node n.
func nextNode(n Node, edges []Edge, payload map[string]any) string {
	switch n.Type {
	case NodeSwitch:
		val, _ := GetField(payload, dataStr(n.Data, "field"))
		want := "case:" + stringify(val)
		var def string
		for _, e := range edges {
			if e.SourceHandle == want {
				return e.Target
			}
			if e.SourceHandle == "default" {
				def = e.Target
			}
		}
		return def
	case NodeCondition:
		branch := "false"
		if evalCondition(payload, n.Data) {
			branch = "true"
		}
		for _, e := range edges {
			if e.SourceHandle == branch {
				return e.Target
			}
		}
		return ""
	default:
		// input / setEvent / setField / unknown: follow the first edge.
		if len(edges) > 0 {
			return edges[0].Target
		}
		return ""
	}
}

// evalCondition evaluates a condition node's {field, op, value}.
func evalCondition(payload map[string]any, data map[string]any) bool {
	field := dataStr(data, "field")
	op := strings.ToLower(strings.TrimSpace(dataStr(data, "op")))
	val, present := GetField(payload, field)
	want := data["value"]

	switch op {
	case "exists":
		return present
	case "notexists", "missing":
		return !present
	case "", "eq", "==":
		return compareEq(val, want)
	case "ne", "!=":
		return !compareEq(val, want)
	case "contains":
		return strings.Contains(stringify(val), stringify(want))
	case "gt", ">", "gte", ">=", "lt", "<", "lte", "<=":
		a, aok := toFloat(val)
		b, bok := toFloat(want)
		if !aok || !bok {
			return false
		}
		switch op {
		case "gt", ">":
			return a > b
		case "gte", ">=":
			return a >= b
		case "lt", "<":
			return a < b
		case "lte", "<=":
			return a <= b
		}
	}
	return false
}

func (r *Result) addEvent(e string) {
	if e == "" {
		return
	}
	for _, x := range r.Events {
		if x == e {
			return
		}
	}
	r.Events = append(r.Events, e)
}

// GetField resolves a dot-path (e.g. "detail.tp") into a nested payload.
func GetField(payload map[string]any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}
	var cur any = payload
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := m[part]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// compareEq compares two values, numerically when both look numeric, else as
// strings. This makes "34" (JSON string) and 34 (number) compare equal, matching
// how Howen sends mixed types.
func compareEq(a, b any) bool {
	if af, aok := toFloat(a); aok {
		if bf, bok := toFloat(b); bok {
			return af == bf
		}
	}
	return stringify(a) == stringify(b)
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

// stringify renders a value for comparison/labels; integers print without a
// trailing ".0" so a float64(34) matches the handle "case:34".
func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func dataStr(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	if v, ok := data[key]; ok {
		return stringify(v)
	}
	return ""
}
