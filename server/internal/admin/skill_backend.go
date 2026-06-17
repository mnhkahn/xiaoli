package admin

import agentskill "xiaoli/server/internal/agent/tool/skill"

const defaultSkillMaxBytes int64 = agentskill.DefaultMaxBytes

type fileSkillBackendConfig = agentskill.BackendConfig
type fileSkillBackend = agentskill.Backend

func newFileSkillBackend(cfg fileSkillBackendConfig) (*fileSkillBackend, error) {
	return agentskill.NewFileBackend(cfg)
}
