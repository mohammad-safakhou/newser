package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// ==========================================================
// Model Router
// ==========================================================

type ModelRouter struct {
	RootModel, ChildModel string
	RootTemp, ChildTemp   float64
}

func (mr ModelRouter) ForDepth(depth int) (model string, temp float64) {
	if depth == 0 {
		return envOr("ROOT_MODEL", mr.RootModel), floatEnv("ROOT_TEMP", mr.RootTemp)
	}
	return envOr("CHILD_MODEL", mr.ChildModel), floatEnv("CHILD_TEMP", mr.ChildTemp)
}

// ==========================================================
// Master Node
// ==========================================================

type MasterNode struct {
	Name      string
	Depth     int
	Cfg       AppConfig
	Agents    *AgentRegistry
	Repo      AgentRunRepository
	Sink      *FileRunSink
	Stream    chan<- StreamItem
	Router    ModelRouter
	Judge     Judge
	RunID     string
	parentRun string
}

type ObservationSummary struct {
	Stage Stage  `json:"stage"`
	Title string `json:"title,omitempty"`
	URL   string `json:"url,omitempty"`
	Note  string `json:"note,omitempty"`
	Err   string `json:"err,omitempty"`
}

type NodeResult struct {
	Actions  int
	Children int
	Report   string
}

func (m *MasterNode) emit(st Stage, title, url, note, errMsg string) {
	it := StreamItem{RunID: m.RunID, Node: m.Name, Stage: st, Title: title, URL: url, Note: note, Err: errMsg, Time: time.Now()}
	if m.Stream != nil {
		select {
		case m.Stream <- it:
		default:
		}
	}
	_ = m.Repo.Save(context.Background(), it)
}

func (m *MasterNode) planOnce(ctx context.Context, topic Topic, observations []ObservationSummary, budget Budget) (PlannerOutput, error) {
	// Snapshot tools
	var tools []MCPToolSummary
	for _, ag := range m.Agents.All() {
		for name, t := range ag.Tools {
			tools = append(tools, MCPToolSummary{Name: name, Description: t.Description, InputSchema: t.InputSchema, Capability: mapToolToCapability(name)})
		}
	}
	model, temp := m.Router.ForDepth(m.Depth)
	planner := OpenAIPlanner{APIKey: envOr("OPENAI_API_KEY", ""), Model: model, Temp: temp}
	in := PlannerInput{RunID: m.RunID, NodeName: m.Name, Depth: m.Depth, Topic: topic, Tools: tools, Observations: observations, Budget: budget}
	m.emit(StagePlan, "planner", "", fmt.Sprintf("model=%s depth=%d", model, m.Depth), "")
	ctx2, cancel := context.WithTimeout(ctx, m.Cfg.NodeTimeout)
	defer cancel()
	return planner.Plan(ctx2, in)
}

// executeDAG runs actions honoring dependencies. It supports two kinds: tool call and spawn_master.
func (m *MasterNode) executeDAG(ctx context.Context, actions []PlannerAction) (int, int, []ObservationSummary) {
	// index actions
	idIdx := map[string]*PlannerAction{}
	for i := range actions {
		if actions[i].ID == "" {
			actions[i].ID = randomID()
		}
		// init maps to avoid nil panics
		if actions[i].Args == nil {
			actions[i].Args = map[string]any{}
		}
		if actions[i].Expect == nil {
			actions[i].Expect = map[string]any{}
		}
		if actions[i].OnFail == nil {
			actions[i].OnFail = map[string]any{}
		}
		idIdx[actions[i].ID] = &actions[i]
	}
	// dependency tracking
	deps := map[string][]string{}
	rev := map[string][]string{}
	for _, a := range actions {
		for _, d := range a.DependsOn {
			deps[a.ID] = append(deps[a.ID], d)
			rev[d] = append(rev[d], a.ID)
		}
	}
	// ready set
	ready := []string{}
	for _, a := range actions {
		if len(a.DependsOn) == 0 {
			ready = append(ready, a.ID)
		}
	}
	sort.Strings(ready)

	// results store (for templating)
	results := map[string]map[string]any{}
	var obs []ObservationSummary
	var doneCount, childCount int
	deadline := time.Now().Add(m.Cfg.NodeTimeout)
	execOne := func(a *PlannerAction) {
		if time.Now().After(deadline) {
			obs = append(obs, ObservationSummary{Stage: StageError, Title: a.ID, Err: "node timeout"})
			return
		}
		switch a.Kind {
		case ActionTool:
			res, err := m.execTool(ctx, *a, results)
			if err != nil {
				m.emit(StageError, a.ID, "", "", err.Error())
				obs = append(obs, ObservationSummary{Stage: StageError, Title: a.ID, Err: err.Error()})
				return
			}
			ok, reason := m.Judge.Assess(ctx, *a, res)
			m.emit(StageVerify, a.ID, str(res["url"]), reason, "")
			if !ok {
				// on_fail: retry or fallback tool
				if r := toInt(a.OnFail["retry"], 0); r > 0 {
					for i := 0; i < r; i++ {
						res, err = m.execTool(ctx, *a, results)
						if err == nil {
							if ok2, _ := m.Judge.Assess(ctx, *a, res); ok2 {
								break
							}
						}
					}
				}
				if !ok && str(a.OnFail["fallback_tool"]) != "" {
					old := a.Tool
					a.Tool = str(a.OnFail["fallback_tool"])
					res, err = m.execTool(ctx, *a, results)
					a.Tool = old
					if err != nil {
						m.emit(StageError, a.ID, "", "fallback failed", err.Error())
						obs = append(obs, ObservationSummary{Stage: StageError, Title: a.ID, Err: err.Error()})
						return
					}
				}
			}
			results[a.ID] = res
			doneCount++
		case ActionSpawn:
			childName := fmt.Sprintf("%s.child.%s", m.Name, a.ID)
			child := &MasterNode{Name: childName, Depth: m.Depth + 1, Cfg: m.Cfg, Agents: m.Agents, Repo: m.Repo, Sink: m.Sink, Stream: m.Stream, Router: m.Router, Judge: m.Judge, RunID: m.RunID, parentRun: m.RunID}
			childCount++
			m.emit(StageSpawn, childName, "", "spawn", "")
			ctx2, cancel := context.WithTimeout(ctx, m.Cfg.NodeTimeout)
			defer cancel()
			_, _ = child.Run(ctx2, *a.SubGoal)
			results[a.ID] = map[string]any{"spawned": true, "node": childName}
			doneCount++
		default:
			m.emit(StageError, a.ID, "", "unknown action kind", "")
		}
	}

	// simple scheduler (serial execution honoring deps)
	visited := map[string]bool{}
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		if visited[id] {
			continue
		}
		visited[id] = true
		a := idIdx[id]
		execOne(a)
		for _, nxt := range rev[id] {
			// check if all deps done
			allDone := true
			for _, d := range deps[nxt] {
				if !visited[d] {
					allDone = false
					break
				}
			}
			if allDone {
				ready = append(ready, nxt)
			}
		}
	}
	return doneCount, childCount, obs
}

func (m *MasterNode) execTool(ctx context.Context, a PlannerAction, results map[string]map[string]any) (map[string]any, error) {
	ag := m.Agents.FindAgentFor(a.Capability)
	if ag == nil {
		return nil, fmt.Errorf("no agent for %s", a.Capability)
	}
	tool := a.Tool
	if tool == "" {
		tool = pickTool(ag, a.Capability)
	}
	args := resolveTemplates(a.Args, results)
	ctx2, cancel := context.WithTimeout(ctx, m.Cfg.ToolTimeout)
	defer cancel()
	res, err := ag.Client.CallTool(ctx2, tool, args)
	if err != nil {
		return nil, err
	}
	if c, ok := res["content"].(map[string]any); ok {
		res = c
	}
	// normalize small fields for judge convenience
	if text, ok := res["text"].(string); ok {
		res["chars"] = len(text)
	}
	m.emit(StageAction, a.ID, str(res["url"]), fmt.Sprintf("%s %s", a.Capability, tool), "")
	return res, nil
}

// Run executes a node: plan → execute DAG → optionally replan until stop or budget/depth/time.
func (m *MasterNode) Run(ctx context.Context, topic Topic) (NodeResult, error) {
	start := time.Now()
	m.emit(StageKickoff, m.Name, "", "start", "")
	budget := Budget{MaxActions: intEnv("MAX_NODE_ACTIONS", m.Cfg.MaxNodeActions), ExpiresAt: time.Now().Add(m.Cfg.NodeTimeout).Format(time.RFC3339)}
	var observations []ObservationSummary
	var totalActions, totalChildren int
	iters := 0
	for {
		iters++
		out, err := m.planOnce(ctx, topic, observations, budget)
		if err != nil {
			m.emit(StageError, "planner", "", "", err.Error())
			break
		}
		if out.Stop {
			m.emit(StageComplete, "planner", "", "stop: "+out.WhyStop, "")
			break
		}
		if len(out.Actions) == 0 {
			m.emit(StageComplete, "planner", "", "no actions", "")
			break
		}
		// enforce depth/budget
		if m.Depth >= intEnv("MAX_NODE_DEPTH", m.Cfg.MaxNodeDepth) {
			m.emit(StageComplete, m.Name, "", "max depth", "")
			break
		}
		if totalActions >= budget.MaxActions {
			m.emit(StageComplete, m.Name, "", "budget spent", "")
			break
		}

		a, c, obs := m.executeDAG(ctx, out.Actions)
		totalActions += a
		totalChildren += c
		observations = append(observations, obs...)
		if time.Since(start) > m.Cfg.NodeTimeout {
			m.emit(StageComplete, m.Name, "", "node timeout", "")
			break
		}
		if totalActions >= budget.MaxActions {
			m.emit(StageComplete, m.Name, "", "budget spent", "")
			break
		}
	}
	// Write summary file
	items, _ := m.Repo.List(ctx, m.RunID)
	_ = m.Sink.Save(ctx, m.RunID, items)
	// Best-effort report
	rep := m.generateReport(ctx, topic)
	m.emit(StageComplete, m.Name, "", fmt.Sprintf("done actions=%d children=%d", totalActions, totalChildren), "")
	return NodeResult{Actions: totalActions, Children: totalChildren, Report: rep}, nil
}

func (m *MasterNode) generateReport(ctx context.Context, topic Topic) string {
	items, _ := m.Repo.List(ctx, m.RunID)
	// collect sources from action logs where we have URLs
	type Src struct{ Title, URL, Note string }
	var srcs []Src
	for _, it := range items {
		if it.URL != "" {
			srcs = append(srcs, Src{Title: it.Title, URL: it.URL, Note: it.Note})
		}
	}
	rin := map[string]any{"run_id": m.RunID, "node": m.Name, "topic": topic, "sources": srcs}
	md := ""
	model := envOr("REPORT_MODEL", "")
	if model != "" && envOr("OPENAI_API_KEY", "") != "" {
		body := map[string]any{"model": model, "temperature": 0.2, "messages": []map[string]any{
			{"role": "system", "content": "Write a concise report: Title, bullets, short summary, and numbered sources (title + URL)."},
			{"role": "user", "content": mustJSON(rin)},
		}}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+envOr("OPENAI_API_KEY", ""))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err == nil && resp.StatusCode/100 == 2 {
			var raw struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&raw)
			_ = resp.Body.Close()
			if len(raw.Choices) > 0 {
				md = strings.TrimSpace(raw.Choices[0].Message.Content)
			}
		}
	}
	if md == "" {
		var b strings.Builder
		b.WriteString("# Report " + m.RunID + "\n\n")
		b.WriteString("Node: " + m.Name + "\n\n")
		b.WriteString("## Topic\n" + topic.Title + "\n\n")
		if len(srcs) > 0 {
			b.WriteString("## Sources\n")
			for i, s := range srcs {
				fmt.Fprintf(&b, "%d. [%s](%s)\n", i+1, s.Title, s.URL)
			}
		}
		md = b.String()
	}
	path := fmt.Sprintf("runs/%s_report.md", m.RunID)
	_ = os.WriteFile(path, []byte(md), 0o644)
	return path
}
