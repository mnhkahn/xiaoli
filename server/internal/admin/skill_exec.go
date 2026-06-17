package admin

import (
	"context"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/schema"

	agentskill "xiaoli/server/internal/agent/tool/skill"
)

const (
	defaultSkillExecTimeout        = agentskill.DefaultExecTimeout
	defaultSkillExecMaxOutputBytes = agentskill.DefaultExecMaxOutputBytes
)

type skillExecConfig = agentskill.ExecConfig
type skillToolArgs = agentskill.ToolArgs

func buildSkillToolDescription(ctx context.Context, skills []einoskill.FrontMatter) string {
	return agentskill.BuildToolDescription(ctx, skills)
}

func buildSkillToolParams(ctx context.Context, defaults map[string]*schema.ParameterInfo) (map[string]*schema.ParameterInfo, error) {
	return agentskill.BuildToolParams(ctx, defaults)
}

func newSkillContentBuilder(cfg skillExecConfig) func(context.Context, einoskill.Skill, string) (string, error) {
	return agentskill.NewContentBuilder(cfg)
}
