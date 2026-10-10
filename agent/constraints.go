package agent

import (
	"strings"

	"github.com/Autumn-27/artex/db"
)

// constraintBlock renders this task's operation constraints (task_constraints) as a
// high-priority block appended to the planner/worker system prompt. allow/deny are
// grouped; empty string when there are no constraints (or ts is nil). The framing
// deliberately puts these ABOVE the exploration/expansion heuristics so a declared
// boundary wins the tug-of-war against "chase another entry surface".
func constraintBlock(ts *db.ExplorationStore) string {
	if ts == nil {
		return ""
	}
	rows, err := ts.ListConstraints()
	if err != nil || len(rows) == 0 {
		return ""
	}
	var allow, deny []string
	for _, c := range rows {
		text := strings.TrimSpace(c.Text)
		if text == "" {
			continue
		}
		if c.Kind == "allow" {
			allow = append(allow, "- "+text)
		} else {
			deny = append(deny, "- "+text)
		}
	}
	if len(allow) == 0 && len(deny) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【操作约束（最高优先级，凌驾于下方一切探索/拓面启发式；每生成一条意图、每执行一个动作前都必须先自检是否违反，违反即不得进行）】：")
	if len(allow) > 0 {
		b.WriteString("\n允许的操作：\n")
		b.WriteString(strings.Join(allow, "\n"))
	}
	if len(deny) > 0 {
		b.WriteString("\n禁止的操作：\n")
		b.WriteString(strings.Join(deny, "\n"))
	}
	b.WriteString("\n（发现约束之外的新目标/新端口/新主机，不等于获得授权：除非它落在上述允许范围内，否则记为 out-of-scope 事实并跳过，不得为其派生意图或执行动作。）")
	return b.String()
}

// globalGuardrailBlock renders the operator's GLOBAL guardrails — a platform-level
// free-text setting shared by every task (系统设置「约束与护栏」) — as a top-priority
// block appended to the planner/worker system prompt. Empty text returns "" so
// nothing is appended (the settings master switch is applied by the caller's
// resolver, which hands us "" when it is off).
//
// Same framing as constraintBlock on purpose: a declared boundary must win the
// tug-of-war against "chase another entry surface", or the planner reads an
// out-of-scope host as just one more thing to try.
func globalGuardrailBlock(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【全局护栏（平台级配置·对所有任务生效·最高优先级，凌驾于下方一切探索/拓面启发式；每生成一条意图、每执行一个动作前都必须先自检是否违反，违反即不得进行）】：\n")
	b.WriteString(text)
	b.WriteString("\n（全局护栏与本任务操作约束同时存在时，以更严格的一条为准；违反任意一条都不得执行。）")
	return b.String()
}
