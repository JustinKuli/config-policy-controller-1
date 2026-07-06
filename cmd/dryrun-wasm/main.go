// Copyright Contributors to the Open Cluster Management project

//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"syscall/js"

	"open-cluster-management.io/config-policy-controller/pkg/dryrun/playground"
)

func main() {
	exports := map[string]any{
		"evaluate": js.FuncOf(evaluate),
		"lint":     js.FuncOf(lintPolicy),
	}
	js.Global().Set("dryrunPlayground", js.ValueOf(exports))

	select {}
}

func evaluate(_ js.Value, args []js.Value) any {
	if len(args) < 2 {
		return encodeError(400, "evaluate requires policy and resources arguments")
	}

	policy := args[0].String()
	resources := args[1].String()

	mappings := ""
	if len(args) > 2 {
		mappings = args[2].String()
	}

	result, err := playground.Evaluate(context.Background(), policy, resources, mappings)
	if err != nil && !playground.IsEvaluateSuccess(err) {
		return encodeError(playground.EvaluateErrorStatus(err), err.Error())
	}

	out, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return encodeError(500, fmt.Sprintf("failed to encode evaluation result: %v", marshalErr))
	}

	return string(out)
}

func lintPolicy(_ js.Value, args []js.Value) any {
	if len(args) < 1 {
		return encodeError(400, "lint requires a policy argument")
	}

	policy := args[0].String()
	issues := playground.LintPolicy(policy)

	out, err := json.Marshal(map[string]any{"issues": issues})
	if err != nil {
		return encodeError(500, fmt.Sprintf("failed to encode lint result: %v", err))
	}

	return string(out)
}

func encodeError(status int, message string) string {
	out, err := json.Marshal(map[string]any{
		"error":  message,
		"status": status,
	})
	if err != nil {
		return fmt.Sprintf(`{"error":%q,"status":%d}`, message, status)
	}

	return string(out)
}
