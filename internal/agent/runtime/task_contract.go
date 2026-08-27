package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
)

// TaskContract is the model-independent definition of what must be evidenced
// before an executor turn can be reported as complete.
type TaskContract struct {
	Goal         string                  `json:"goal"`
	Requirements []CapabilityRequirement `json:"requirements"`
	Verification string                  `json:"verification"`
	Risk         string                  `json:"risk"`
	Source       string                  `json:"source,omitempty"`
}

type CapabilityRequirement struct {
	Capability string `json:"capability"`
	Required   bool   `json:"required"`
}

type ToolEvidence struct {
	Tool         string   `json:"tool"`
	Capabilities []string `json:"capabilities"`
	RecordedAt   string   `json:"recorded_at"`
}

type TaskContractState struct {
	Contract  TaskContract   `json:"contract"`
	Evidence  []ToolEvidence `json:"evidence,omitempty"`
	Status    string         `json:"status"` // active, complete, unverified
	Attempts  int            `json:"attempts"`
	UpdatedAt string         `json:"updated_at"`
}

const maxContractContinuations = 3

type taskContractContinuationKey struct{}

func localTaskContract(userText string) (TaskContract, bool) {
	text := strings.ToLower(strings.TrimSpace(userText))
	if text == "" {
		return TaskContract{}, false
	}
	if strings.Contains(text, "堆栈") || strings.Contains(text, "stack trace") || strings.Contains(text, "stacktrace") {
		return TaskContract{Goal: userText, Requirements: []CapabilityRequirement{
			{Capability: "observability.error_stack", Required: true},
			{Capability: "observability.error_context", Required: true},
		}, Verification: "查询结果必须包含 errorStack、errorMessage、pageUrl，或明确字段不可用", Risk: "read_only", Source: "template"}, true
	}
	if strings.Contains(text, "跑测试") || strings.Contains(text, "运行测试") || strings.Contains(text, "run test") {
		return TaskContract{Goal: userText, Requirements: []CapabilityRequirement{{Capability: "code.tests_passed", Required: true}}, Verification: "真实测试命令必须成功退出", Risk: "read_only", Source: "template"}, true
	}
	if strings.Contains(text, "修复") || strings.Contains(text, "fix ") || strings.Contains(text, "修一下") {
		return TaskContract{Goal: userText, Requirements: []CapabilityRequirement{
			{Capability: "code.change_applied", Required: true}, {Capability: "code.tests_passed", Required: true},
		}, Verification: "必须有真实写入证据和成功的相关测试", Risk: "write", Source: "template"}, true
	}
	return TaskContract{}, false
}

func conservativeTaskContract(userText string) TaskContract {
	return TaskContract{Goal: userText, Requirements: []CapabilityRequirement{{Capability: "external.evidence_required", Required: true}}, Verification: "需要真实工具证据；若不可获得，必须显示未验证完成", Risk: "unknown", Source: "fallback"}
}

func normalizeTaskContract(raw string) (TaskContract, error) {
	var contract TaskContract
	if err := json.Unmarshal([]byte(raw), &contract); err != nil {
		return TaskContract{}, fmt.Errorf("invalid task contract JSON: %w", err)
	}
	contract.Goal = strings.TrimSpace(contract.Goal)
	contract.Verification = strings.TrimSpace(contract.Verification)
	contract.Risk = strings.TrimSpace(contract.Risk)
	if contract.Goal == "" || contract.Verification == "" || contract.Risk == "" || len(contract.Requirements) == 0 {
		return TaskContract{}, fmt.Errorf("task contract requires goal, requirements, verification, and risk")
	}
	seen := map[string]bool{}
	valid := make([]CapabilityRequirement, 0, len(contract.Requirements))
	for _, requirement := range contract.Requirements {
		requirement.Capability = strings.TrimSpace(requirement.Capability)
		if requirement.Capability == "" || seen[requirement.Capability] {
			continue
		}
		seen[requirement.Capability] = true
		valid = append(valid, requirement)
	}
	if len(valid) == 0 {
		return TaskContract{}, fmt.Errorf("task contract has no valid requirements")
	}
	contract.Requirements = valid
	contract.Source = "planner"
	return contract, nil
}

func (a *Agent) planTaskContract(ctx context.Context, userText string) TaskContract {
	if contract, ok := localTaskContract(userText); ok {
		return contract
	}
	model, err := a.plannerChatModel(ctx)
	if err != nil {
		return conservativeTaskContract(userText)
	}
	prompt := `你是 TaskContract Planner。不得调用工具，不得解释，只返回符合以下 JSON Schema 的对象：
{"goal":"string","requirements":[{"capability":"dot.separated.name","required":true}],"verification":"string","risk":"read_only|write|external|unknown"}
requirements 只写需要真实工具证据才能满足的能力；不能臆造工具或字段。`
	response, err := model.Generate(ctx, []*schema.Message{schema.SystemMessage(prompt), schema.UserMessage(userText)})
	if err != nil || response == nil {
		return conservativeTaskContract(userText)
	}
	contract, err := normalizeTaskContract(response.Content)
	if err != nil {
		return conservativeTaskContract(userText)
	}
	return contract
}

// plannerChatModel intentionally does not use the executor's cached model:
// planning is a short, deterministic JSON task with a smaller output budget.
func (a *Agent) plannerChatModel(ctx context.Context) (*openai.ChatModel, error) {
	modelID := a.CurrentLLMModel()
	modelCfg := a.cfg.selectedLLMModelConfigFor(modelID)
	if modelCfg.Model == "" || modelCfg.BaseURL == "" || modelCfg.APIKey == "" {
		return nil, fmt.Errorf("planner model %q is incomplete", modelID)
	}
	baseURL := strings.TrimRight(strings.TrimSuffix(modelCfg.BaseURL, "/chat/completions"), "/")
	temperature := float32(0)
	maxTokens := 800
	return openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL:     baseURL,
		APIKey:      modelCfg.APIKey,
		Model:       modelCfg.Model,
		HTTPClient:  newLLMHTTPClient(a.cfg.LLMTimeout, llmResponseHeaderTimeout),
		Temperature: &temperature,
		MaxTokens:   &maxTokens,
	})
}

func (s *TaskContractState) mergeEvidence(evidence []ToolEvidence) {
	if s == nil {
		return
	}
	for _, item := range evidence {
		if len(item.Capabilities) > 0 {
			s.Evidence = append(s.Evidence, item)
		}
	}
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

func (s TaskContractState) provided() map[string]bool {
	provided := map[string]bool{}
	for _, evidence := range s.Evidence {
		for _, capability := range evidence.Capabilities {
			provided[capability] = true
		}
	}
	return provided
}

func (s TaskContractState) missing() []string {
	provided := s.provided()
	var missing []string
	for _, requirement := range s.Contract.Requirements {
		if requirement.Required && !provided[requirement.Capability] && !provided[requirement.Capability+".unavailable"] {
			missing = append(missing, requirement.Capability)
		}
	}
	return missing
}

func taskContractExecutorInstruction(contract TaskContractState) string {
	data, _ := json.Marshal(contract.Contract)
	missing := contract.missing()
	return "内部执行控制（绝不可在面向用户的回复中提及或复述）：\n" +
		"必须通过真实工具结果满足以下内部合同，不能自行宣布完成；优先补足缺失项。\n" +
		"合同：" + string(data) + "\n" +
		"缺失项：" + strings.Join(missing, ", ") + "\n\n" +
		"最终回复必须面向用户：先给结论，再说明为什么/采用的思路、实际改动（若有）和简明验证方式。" +
		"不得提及合同、能力、证据、内部校验、工具名、原始工具输出或内部字段名。"
}

func taskContractStopMessage(reply string, contract TaskContract) string {
	message := "\n\n我还无法根据实际执行结果确认：" + contract.Verification + "。为避免误报，我先不将此标记为已完成。"
	if strings.TrimSpace(reply) == "" {
		return strings.TrimSpace(message)
	}
	return strings.TrimSpace(reply) + message
}

// capabilitiesFromToolOutput is deliberately conservative: it only grants a
// capability when the successful, unmodified tool result contains the
// corresponding concrete signal. Models never create evidence themselves.
func capabilitiesFromToolOutput(toolName, arguments, result string) []string {
	name := strings.ToLower(toolName)
	args := strings.ToLower(arguments)
	body := strings.ToLower(result)
	var capabilities []string
	if strings.Contains(body, "errorstack") || strings.Contains(body, "error_stack") || strings.Contains(body, "stacktrace") {
		capabilities = append(capabilities, "observability.error_stack")
	}
	if (strings.Contains(body, "errormessage") || strings.Contains(body, "error_message") || strings.Contains(body, "message")) &&
		(strings.Contains(body, "pageurl") || strings.Contains(body, "page_url") || strings.Contains(body, "url")) {
		capabilities = append(capabilities, "observability.error_context")
	}
	// A query may truthfully establish that New Relic did not expose a requested
	// field. That is verification of unavailability, not invented stack data.
	if strings.Contains(body, "unavailable") || strings.Contains(body, "not available") || strings.Contains(body, "不可用") || strings.Contains(body, "不存在") {
		if strings.Contains(body, "errorstack") || strings.Contains(body, "error_stack") {
			capabilities = append(capabilities, "observability.error_stack.unavailable")
		}
		if strings.Contains(body, "errormessage") || strings.Contains(body, "error_message") || strings.Contains(body, "pageurl") || strings.Contains(body, "page_url") {
			capabilities = append(capabilities, "observability.error_context.unavailable")
		}
	}
	if strings.Contains(name, "bash") && (strings.Contains(args, "go test") || strings.Contains(args, "npm test") || strings.Contains(args, "pytest") || strings.Contains(args, "cargo test")) &&
		!strings.Contains(body, "fail") && !strings.Contains(body, "error:") {
		capabilities = append(capabilities, "code.tests_passed")
	}
	if strings.Contains(name, "file_write") || strings.Contains(name, "edit_file") {
		capabilities = append(capabilities, "code.change_applied")
	}
	return capabilities
}
