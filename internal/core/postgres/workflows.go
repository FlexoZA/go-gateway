package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dfm/device-gateway/internal/core/flow"
)

// workflows.go backs the per-model visual device-mapping workflows. The admin
// panel edits a node graph per (unit, model); the gateway loads the active ones
// and the flow engine evaluates them. Storage methods live here; the engine is
// internal/core/flow.

// ListWorkflows returns summaries (no full graph) of a unit's workflows.
func (s *Store) ListWorkflows(ctx context.Context, unit string) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT model, name, is_active, updated_at,
		        COALESCE(jsonb_array_length(graph->'nodes'), 0),
		        COALESCE(jsonb_array_length(graph->'edges'), 0)
		 FROM mapping_workflows WHERE unit = $1 ORDER BY model`, unit)
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var model, name string
		var active bool
		var updatedAt any
		var nodeCount, edgeCount int
		if err := rows.Scan(&model, &name, &active, &updatedAt, &nodeCount, &edgeCount); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"model":      model,
			"name":       name,
			"is_active":  active,
			"updated_at": updatedAt,
			"node_count": nodeCount,
			"edge_count": edgeCount,
		})
	}
	return out, rows.Err()
}

// GetWorkflow returns one model's full workflow (graph included). Returns
// ErrNotFound when the model has no workflow.
func (s *Store) GetWorkflow(ctx context.Context, unit, model string) (map[string]any, error) {
	var name string
	var active bool
	var updatedAt any
	var graph []byte
	err := s.pool.QueryRow(ctx,
		`SELECT name, is_active, updated_at, graph FROM mapping_workflows WHERE unit = $1 AND model = $2`,
		unit, model).Scan(&name, &active, &updatedAt, &graph)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return map[string]any{
		"unit":       unit,
		"model":      model,
		"name":       name,
		"is_active":  active,
		"updated_at": updatedAt,
		"graph":      json.RawMessage(graph),
	}, nil
}

// UpsertWorkflow creates or replaces a model's workflow. The graph is validated
// (single input node, edges reference real nodes) before it is stored, so the
// gateway never loads a structurally broken graph. Firing the NOTIFY makes the
// running gateway reload within milliseconds.
func (s *Store) UpsertWorkflow(ctx context.Context, unit, model, name string, graph json.RawMessage, isActive bool) error {
	unit = strings.TrimSpace(unit)
	model = strings.TrimSpace(model)
	if unit == "" || model == "" {
		return errors.New("unit and model are required")
	}
	var g flow.Graph
	if err := json.Unmarshal(graph, &g); err != nil {
		return fmt.Errorf("invalid graph JSON: %w", err)
	}
	if err := g.Validate(); err != nil {
		return fmt.Errorf("invalid graph: %w", err)
	}
	// Re-marshal canonically so what we store is exactly what the engine parses.
	canonical, err := json.Marshal(g)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO mapping_workflows (unit, model, name, graph, is_active, updated_at)
		 VALUES ($1, $2, $3, $4, $5, now())
		 ON CONFLICT (unit, model) DO UPDATE
		   SET name = EXCLUDED.name, graph = EXCLUDED.graph,
		       is_active = EXCLUDED.is_active, updated_at = now()`,
		unit, model, name, canonical, isActive)
	return err
}

// DeleteWorkflow removes a model's workflow (that model reverts to the built-in
// table/defaults). ErrNotFound when absent.
func (s *Store) DeleteWorkflow(ctx context.Context, unit, model string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM mapping_workflows WHERE unit = $1 AND model = $2`, unit, model)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LoadActiveWorkflows returns every active, structurally-valid workflow for a
// unit, keyed by model, ready for the engine. Invalid graphs are skipped (the
// model falls back to the built-in mapping) rather than failing the whole load.
func (s *Store) LoadActiveWorkflows(ctx context.Context, unit string) (map[string]*flow.Graph, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT model, graph FROM mapping_workflows WHERE unit = $1 AND is_active`, unit)
	if err != nil {
		return nil, fmt.Errorf("load workflows: %w", err)
	}
	defer rows.Close()

	out := map[string]*flow.Graph{}
	for rows.Next() {
		var model string
		var raw []byte
		if err := rows.Scan(&model, &raw); err != nil {
			return nil, err
		}
		var g flow.Graph
		if err := json.Unmarshal(raw, &g); err != nil {
			continue
		}
		if g.Validate() != nil {
			continue
		}
		out[model] = &g
	}
	return out, rows.Err()
}

// ListenForWorkflowChanges mirrors ListenForMappingChanges for the workflow
// channel: it invokes onChange on every (re)connect and on every NOTIFY, so
// per-model workflow edits apply instantly.
func (s *Store) ListenForWorkflowChanges(ctx context.Context, onChange func(unit string)) {
	for ctx.Err() == nil {
		if err := s.listenChannel(ctx, workflowChangeChannel, onChange); err != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}
