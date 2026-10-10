package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// 总览「约束管理」的人工 CRUD 接口 + 注入范围开关的解析。操作约束(allow/deny)与 agent 侧的
// set_constraints 工具写同一张 task_constraints 表;这里是人类在 UI 上直接增删改。约束仅是
// 提示上下文——增删改后【不】通知 planner,下一轮规划自然读库生效(按产品决策)。每个变更 handler
// 都走 beginTaskOperation/decInflight,避免与任务删除竞态(与目标/意图 CRUD 一致)。

// 注入范围开关的 settings key,默认都开(GetBool 第二参数 = true)。
const (
	settingConstraintsInjectPlanner = "constraints_inject_planner"
	settingConstraintsInjectWorker  = "constraints_inject_worker"
	// 约束与护栏总开关(默认开):关闭后「全局护栏」与所有任务的操作约束一律不注入任何 agent。
	settingGuardrailsEnabled = "guardrails_enabled"
	// 全局护栏自定义文本:平台级自由文本,对所有任务生效,逐字拼进 planner/worker 系统提示。
	settingGuardrailsText = "guardrails_text"
	// 全局护栏文本上限(字节),防止一次粘贴把每轮系统提示撑爆。
	guardrailsTextMaxLen = 20000
)

// constraintInjectPlanner / constraintInjectWorker 报告是否把操作约束注入对应 agent 的
// 系统提示(默认开)。作为 resolver 传给 planner/worker,每轮读 → 改开关即时生效。
func (s *Server) constraintInjectPlanner() bool {
	// 总开关关闭 = 一律不注入,不再看单 agent 开关。
	return s.guardrailsEnabled() && s.m.pg.GetBool(settingConstraintsInjectPlanner, true)
}

func (s *Server) constraintInjectWorker() bool {
	return s.guardrailsEnabled() && s.m.pg.GetBool(settingConstraintsInjectWorker, true)
}

// listConstraints 返回本任务的全部操作约束(allow 在前、deny 在后)。
func (s *Server) listConstraints(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	rows, err := t.Store.ListConstraints()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"constraints": constraintDTOs(rows)})
}

// addConstraint 人工新增一条操作约束(kind=allow|deny)。不通知 planner。
func (s *Server) addConstraint(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	if !s.engine.beginTaskOperation(t.ID) {
		writeErr(w, 409, "任务正在删除,无法新增约束")
		return
	}
	defer s.engine.decInflight(t.ID)

	var body struct {
		Text string `json:"text"`
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		writeErr(w, 400, "约束内容不能为空")
		return
	}
	kind := normalizeConstraintKind(body.Kind)
	if kind == "" {
		writeErr(w, 400, "kind 必须是 allow 或 deny")
		return
	}
	id, err := t.Store.AddConstraint(kind, text, "human")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, ConstraintDTO{ID: strconv.FormatInt(id, 10), Kind: kind, Text: text, Origin: "human"})
}

// editConstraint 人工修改一条约束(kind + text)。不通知 planner。
func (s *Server) editConstraint(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	if !s.engine.beginTaskOperation(t.ID) {
		writeErr(w, 409, "任务正在删除,无法修改约束")
		return
	}
	defer s.engine.decInflight(t.ID)

	cid, err := strconv.ParseInt(r.PathValue("cid"), 10, 64)
	if err != nil || cid <= 0 {
		writeErr(w, 400, "bad constraint id")
		return
	}
	var body struct {
		Text string `json:"text"`
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		writeErr(w, 400, "约束内容不能为空")
		return
	}
	kind := normalizeConstraintKind(body.Kind)
	if kind == "" {
		writeErr(w, 400, "kind 必须是 allow 或 deny")
		return
	}
	if err := t.Store.UpdateConstraint(cid, kind, text); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, ConstraintDTO{ID: strconv.FormatInt(cid, 10), Kind: kind, Text: text})
}

// deleteConstraint 人工删除一条约束。不通知 planner。
func (s *Server) deleteConstraint(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	if !s.engine.beginTaskOperation(t.ID) {
		writeErr(w, 409, "任务正在删除,无法删除约束")
		return
	}
	defer s.engine.decInflight(t.ID)

	cid, err := strconv.ParseInt(r.PathValue("cid"), 10, 64)
	if err != nil || cid <= 0 {
		writeErr(w, 400, "bad constraint id")
		return
	}
	if err := t.Store.DeleteConstraint(cid); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// normalizeConstraintKind lowercases + validates the kind; "" on invalid.
func normalizeConstraintKind(k string) string {
	k = strings.TrimSpace(strings.ToLower(k))
	if k == "allow" || k == "deny" {
		return k
	}
	return ""
}

// constraintDTOs converts db rows to the frontend shape.
func constraintDTOs(in []db.Constraint) []ConstraintDTO {
	out := make([]ConstraintDTO, 0, len(in))
	for _, c := range in {
		out = append(out, ConstraintDTO{
			ID:     strconv.FormatInt(c.ID, 10),
			Kind:   c.Kind,
			Text:   c.Text,
			Origin: c.Origin,
			TS:     rfc3339(c.CreatedAt),
		})
	}
	return out
}

// guardrailsEnabled reports the 约束与护栏 master switch (default on).
func (s *Server) guardrailsEnabled() bool {
	return s.m.pg.GetBool(settingGuardrailsEnabled, true)
}

// guardrailsText returns the stored global guardrail text, ignoring the master
// switch — the settings UI must still show (and let you edit) the text while the
// switch is off. Use globalGuardrails for what actually gets injected.
func (s *Server) guardrailsText() string {
	v, _, _ := s.m.pg.GetSetting(settingGuardrailsText)
	return v
}

// globalGuardrails returns the text that ACTUALLY gets injected: empty when the
// master switch is off, so "off" means one thing everywhere.
func (s *Server) globalGuardrails() string {
	if !s.guardrailsEnabled() {
		return ""
	}
	return s.guardrailsText()
}

// listAllConstraints returns every task's operation constraints plus the current
// global guardrails, for the 系统设置「约束与护栏」overview. Read-only aggregate: a
// single constraint is still added/edited/deleted on its own task.
func (s *Server) listAllConstraints(w http.ResponseWriter, r *http.Request) {
	_ = r
	rows, err := s.m.pg.ListAllConstraints()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]TaskConstraintDTO, 0, len(rows))
	for _, c := range rows {
		dto := TaskConstraintDTO{
			ConstraintDTO: ConstraintDTO{
				ID:     strconv.FormatInt(c.ID, 10),
				Kind:   c.Kind,
				Text:   c.Text,
				Origin: c.Origin,
				TS:     rfc3339(c.CreatedAt),
			},
			TaskName: c.TaskName,
			TaskGoal: c.TaskGoal,
		}
		if c.TaskID > 0 {
			dto.TaskID = strconv.FormatInt(c.TaskID, 10)
		}
		out = append(out, dto)
	}
	writeJSON(w, 200, map[string]any{
		"constraints":        out,
		"guardrails":         s.globalGuardrails(),
		"guardrails_text":    s.guardrailsText(),
		"guardrails_enabled": s.guardrailsEnabled(),
	})
}
