package agent

import (
	"github.com/cloudwego/eino/components/tool"
	"mineagent/internal/tools"
)

func (a *Agent) withSkills(req Request) Request {
	catalog := tools.SharedSkillCatalog(a.skillsDir)
	if catalog == "" {
		return req
	}
	instruction := req.SystemInstruction
	if instruction == "" {
		instruction = a.instruction
	}
	req.SystemInstruction = instruction + "\n\n" + catalog
	agentTools := req.Tools
	if len(agentTools) == 0 {
		agentTools = a.defaultTools
		// Adding optional skill discovery must not change public MC's budget.
		if req.BodyPrincipal == nil && (req.SessionKey == a.sessionKey || req.SessionKey == "") && req.MaxIterations == 0 {
			req.MaxIterations = 8
		}
	}
	req.Tools = append(append([]tool.BaseTool(nil), agentTools...), tools.SharedSkillTool(a.skillsDir))
	return req
}
