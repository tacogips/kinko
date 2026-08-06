package kinko

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type syncBootstrapResult struct {
	Created    int              `json:"created"`
	Unchanged  int              `json:"unchanged"`
	Conflicts  int              `json:"conflicts"`
	Applied    bool             `json:"applied"`
	PlanDigest string           `json:"plan_digest"`
	Guidance   string           `json:"guidance"`
	Actions    []syncResultItem `json:"actions"`
}

const syncBootstrapRecoveryGuidance = "The source namespace is only an orphan candidate; pruning requires an explicit retired-machine acknowledgement."

func bootstrapResultForPlan(plan *syncBootstrapPlan, applied bool) syncBootstrapResult {
	result := syncBootstrapResult{Applied: applied, PlanDigest: plan.PlanDigest, Conflicts: len(plan.Conflicts), Guidance: syncBootstrapRecoveryGuidance, Actions: []syncResultItem{}}
	for _, action := range plan.Actions {
		item := syncResultItem{Action: syncActionKindName(action.Kind), Profile: action.Identity.Profile, Scope: string(action.Identity.Scope), Key: action.Identity.Key, Reason: action.Reason}
		item.Path = strings.TrimPrefix(strings.TrimPrefix(action.Identity.Path, "local:"), "logical:")
		result.Actions = append(result.Actions, item)
		switch action.Kind {
		case syncActionCreate, syncActionUpdate:
			result.Created++
		case syncActionUnchanged, syncActionIgnore:
			result.Unchanged++
		}
	}
	return result
}

func printSyncBootstrapResult(writer io.Writer, result syncBootstrapResult, jsonOutput bool) error {
	if writer == nil {
		return fmt.Errorf("bootstrap output writer is nil")
	}
	if jsonOutput {
		encoder := json.NewEncoder(writer)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(result)
	}
	mode := "preview"
	if result.Applied {
		mode = "applied"
	}
	for _, action := range result.Actions {
		location := action.Scope
		if action.Scope == string(scopeKindPath) {
			location = fmt.Sprintf("profile=%q path=%q", action.Profile, action.Path)
		}
		if _, err := fmt.Fprintf(writer, "%s %s / %s: %s\n", action.Action, location, action.Key, action.Reason); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(writer, "bootstrap=%s created=%d unchanged=%d conflicts=%d plan=%s\n", mode, result.Created, result.Unchanged, result.Conflicts, result.PlanDigest); err != nil {
		return err
	}
	_, err := fmt.Fprintln(writer, result.Guidance)
	return err
}
