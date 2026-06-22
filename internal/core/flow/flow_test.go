package flow

import (
	"encoding/json"
	"testing"
)

// buildGraph models: input → switch(eventCode) → [case 30 → switch(detail.tp) →
// case 34 → setEvent AI:CELLPHONE → output] [default → setEvent ALARM → output].
func buildGraph() *Graph {
	return &Graph{
		Nodes: []Node{
			{ID: "in", Type: NodeInput},
			{ID: "sw1", Type: NodeSwitch, Data: map[string]any{"field": "eventCode"}},
			{ID: "sw2", Type: NodeSwitch, Data: map[string]any{"field": "detail.tp"}},
			{ID: "phone", Type: NodeSetEvent, Data: map[string]any{"event": "AI:CELLPHONE"}},
			{ID: "alarm", Type: NodeSetEvent, Data: map[string]any{"event": "ALARM"}},
			{ID: "out1", Type: NodeOutput},
			{ID: "out2", Type: NodeOutput},
		},
		Edges: []Edge{
			{ID: "e1", Source: "in", Target: "sw1"},
			{ID: "e2", Source: "sw1", SourceHandle: "case:30", Target: "sw2"},
			{ID: "e3", Source: "sw1", SourceHandle: "default", Target: "alarm"},
			{ID: "e4", Source: "sw2", SourceHandle: "case:34", Target: "phone"},
			{ID: "e5", Source: "sw2", SourceHandle: "default", Target: "alarm"},
			{ID: "e6", Source: "phone", Target: "out1"},
			{ID: "e7", Source: "alarm", Target: "out2"},
		},
	}
}

func TestSwitchPathMatch(t *testing.T) {
	g := buildGraph()
	// eventCode 30 + detail.tp 34 → AI:CELLPHONE
	res, err := g.Evaluate(map[string]any{"eventCode": float64(30), "detail": map[string]any{"tp": float64(34)}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Matched {
		t.Fatalf("expected match, trace=%v", res.Trace)
	}
	if len(res.Events) != 1 || res.Events[0] != "AI:CELLPHONE" {
		t.Fatalf("events = %v, want [AI:CELLPHONE]", res.Events)
	}
}

func TestSwitchStringNumberCoercion(t *testing.T) {
	g := buildGraph()
	// Howen often sends codes as strings: "30" / "34" must still match case:30 / case:34.
	res, _ := g.Evaluate(map[string]any{"eventCode": "30", "detail": map[string]any{"tp": "34"}})
	if !res.Matched || res.Events[0] != "AI:CELLPHONE" {
		t.Fatalf("string codes did not match: %+v", res)
	}
}

func TestSwitchDefaultBranch(t *testing.T) {
	g := buildGraph()
	res, _ := g.Evaluate(map[string]any{"eventCode": float64(99)})
	if !res.Matched || res.Events[0] != "ALARM" {
		t.Fatalf("default branch failed: %+v", res)
	}
}

func TestConditionAndSetField(t *testing.T) {
	g := &Graph{
		Nodes: []Node{
			{ID: "in", Type: NodeInput},
			{ID: "c", Type: NodeCondition, Data: map[string]any{"field": "speed", "op": "gt", "value": float64(0)}},
			{ID: "moving", Type: NodeSetEvent, Data: map[string]any{"event": "MOVING"}},
			{ID: "tag", Type: NodeSetField, Data: map[string]any{"key": "reviewed", "value": true}},
			{ID: "out", Type: NodeOutput},
		},
		Edges: []Edge{
			{ID: "e1", Source: "in", Target: "c"},
			{ID: "e2", Source: "c", SourceHandle: "true", Target: "moving"},
			{ID: "e3", Source: "moving", Target: "tag"},
			{ID: "e4", Source: "tag", Target: "out"},
		},
	}
	res, _ := g.Evaluate(map[string]any{"speed": float64(12)})
	if !res.Matched || res.Events[0] != "MOVING" {
		t.Fatalf("condition true path failed: %+v", res)
	}
	if res.Fields["reviewed"] != true {
		t.Fatalf("setField failed: %+v", res.Fields)
	}
	// speed 0 → condition false → no edge → unmatched, no events.
	res2, _ := g.Evaluate(map[string]any{"speed": float64(0)})
	if res2.Matched || len(res2.Events) != 0 {
		t.Fatalf("condition false should not match: %+v", res2)
	}
}

func TestMultipleSetEventsAccumulate(t *testing.T) {
	g := &Graph{
		Nodes: []Node{
			{ID: "in", Type: NodeInput},
			{ID: "a", Type: NodeSetEvent, Data: map[string]any{"event": "SPEEDING"}},
			{ID: "b", Type: NodeSetEvent, Data: map[string]any{"event": "HARSH:BRAKING"}},
			{ID: "dup", Type: NodeSetEvent, Data: map[string]any{"event": "SPEEDING"}},
			{ID: "out", Type: NodeOutput},
		},
		Edges: []Edge{
			{ID: "e1", Source: "in", Target: "a"},
			{ID: "e2", Source: "a", Target: "b"},
			{ID: "e3", Source: "b", Target: "dup"},
			{ID: "e4", Source: "dup", Target: "out"},
		},
	}
	res, _ := g.Evaluate(nil)
	if len(res.Events) != 2 || res.Events[0] != "SPEEDING" || res.Events[1] != "HARSH:BRAKING" {
		t.Fatalf("accumulation/dedupe failed: %v", res.Events)
	}
}

func TestValidate(t *testing.T) {
	if err := (&Graph{Nodes: []Node{{ID: "a", Type: NodeSwitch}}}).Validate(); err == nil {
		t.Fatal("expected error for missing input node")
	}
	dup := &Graph{Nodes: []Node{{ID: "x", Type: NodeInput}, {ID: "x", Type: NodeOutput}}}
	if err := dup.Validate(); err == nil {
		t.Fatal("expected duplicate id error")
	}
	bad := &Graph{Nodes: []Node{{ID: "in", Type: NodeInput}}, Edges: []Edge{{ID: "e", Source: "in", Target: "ghost"}}}
	if err := bad.Validate(); err == nil {
		t.Fatal("expected unknown target error")
	}
	if err := buildGraph().Validate(); err != nil {
		t.Fatalf("valid graph rejected: %v", err)
	}
}

func TestCycleDetected(t *testing.T) {
	g := &Graph{
		Nodes: []Node{{ID: "in", Type: NodeInput}, {ID: "a", Type: NodeSetEvent, Data: map[string]any{"event": "X"}}},
		Edges: []Edge{{ID: "e1", Source: "in", Target: "a"}, {ID: "e2", Source: "a", Target: "in"}},
	}
	if _, err := g.Evaluate(nil); err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	g := buildGraph()
	b, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	var g2 Graph
	if err := json.Unmarshal(b, &g2); err != nil {
		t.Fatal(err)
	}
	res, _ := g2.Evaluate(map[string]any{"eventCode": float64(30), "detail": map[string]any{"tp": float64(34)}})
	if !res.Matched || res.Events[0] != "AI:CELLPHONE" {
		t.Fatalf("round-tripped graph misbehaved: %+v", res)
	}
}
