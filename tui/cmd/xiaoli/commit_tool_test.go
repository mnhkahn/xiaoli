package main

import "testing"

func TestPlanModeDisablesCommitTool(t *testing.T) {
	for _, name := range disabledToolsForPlanMode(true) {
		if name == "commit" {
			return
		}
	}
	t.Fatal("plan mode does not disable commit")
}
