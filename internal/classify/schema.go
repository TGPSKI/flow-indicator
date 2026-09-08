package classify

import "sort"

// outputSchema bounds decoding as well as validating afterwards. Endpoints
// opt in through configuration; an unsupported schema request fails visibly.
func outputSchema(byteLength int) map[string]any {
	str := map[string]any{"type": "string"}
	boolean := map[string]any{"type": "boolean"}
	count := map[string]any{"type": "integer", "minimum": 0}
	offset := map[string]any{"type": "integer", "minimum": 0, "maximum": byteLength}
	enum := func(values ...string) map[string]any { return map[string]any{"type": "string", "enum": values} }
	array := func(item map[string]any) map[string]any { return map[string]any{"type": "array", "items": item} }
	object := func(props map[string]any) map[string]any {
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return map[string]any{"type": "object", "properties": props, "required": keys, "additionalProperties": false}
	}
	target := enum("node", "alias", "file", "path", "task", "temporal", "quote", "namespace", "relation", "operation", "unknown")
	return object(map[string]any{
		"segments":    array(object(map[string]any{"label": enum("forward_work", "new_task", "new_evidence", "correction", "scope_constraint", "negative_constraint", "positive_constraint", "stop_condition", "meta_process", "handoff", "handoff_after_failure", "referent_disambiguation", "temporal_disambiguation", "namespace_disambiguation", "restart_reconstruction", "restated_prior_state", "other"), "start": offset, "end": offset})),
		"pointer":     object(map[string]any{"is_pointer": boolean, "type": target, "text": str}),
		"correction":  object(map[string]any{"is_correction": boolean, "target_type": target, "target_key": str}),
		"obligations": array(object(map[string]any{"key": str, "kind": enum("stop", "scope", "negative", "positive"), "text": str})),
		"resolutions": array(object(map[string]any{"key": str, "kind": enum("released", "superseded", "satisfied"), "evidence": str})),
		"repair":      object(map[string]any{"target_repaired": map[string]any{"type": []string{"boolean", "null"}}, "new_scope": count, "new_tasks": count, "new_validation": count, "new_constraints": count}),
		"confidence":  map[string]any{"type": "number", "minimum": 0, "maximum": 1},
	})
}
