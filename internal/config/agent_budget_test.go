package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnlimitedAgentBudgetsSurviveLoading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"agent":{"qqMaxIterations":0,"runTimeoutSec":0,"maxConcurrentRuns":1},"workspace":{"execTimeoutSec":0}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Agent.QQMaxIterations != 0 || c.Agent.RunTimeoutSec != 0 || c.Workspace.ExecTimeoutSec != 0 || c.Agent.MaxConcurrentRuns != 1 || c.QQ.ReasoningEffort != "high" || !c.QQ.StreamProgress {
		t.Fatalf("budget/high defaults were lost: %+v / %+v", c.Agent, c.QQ)
	}
}
