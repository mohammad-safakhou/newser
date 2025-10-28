package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/newser/internal/agent/telemetry"
	plannerv1 "github.com/mohammad-safakhou/newser/internal/planner"
)

const (
	templateSuggestionThreshold   = 3
	templateStatusPendingApproval = "pending_approval"
)

func (p *Planner) annotateProceduralTemplates(ctx context.Context, thought UserThought, plan *PlanningResult) {
	if p == nil || p.templates == nil || plan == nil || plan.Graph == nil {
		return
	}
	topicID := strings.TrimSpace(thought.TopicID)
	if topicID == "" {
		return
	}
	doc := plan.Graph
	taskIndex := make(map[string]plannerv1.PlanTask, len(doc.Tasks))
	for _, task := range doc.Tasks {
		taskIndex[task.ID] = task
	}
	var suggestions []map[string]interface{}
	for _, stage := range doc.ExecutionLayers {
		if len(stage.Tasks) == 0 {
			continue
		}
		fingerprint, graphJSON, paramsJSON, meta, err := computeStageFingerprint(stage, taskIndex)
		if err != nil {
			p.logger.Printf("warn: compute procedural template fingerprint: %v", err)
			continue
		}
		state, err := p.templates.UpsertFingerprint(ctx, TemplateFingerprint{
			TopicID:          topicID,
			Fingerprint:      fingerprint,
			Stage:            stage.Stage,
			TaskIDs:          append([]string(nil), stage.Tasks...),
			SampleGraph:      graphJSON,
			SampleParameters: paramsJSON,
			Metadata:         meta,
		})
		if err != nil {
			p.logger.Printf("warn: upsert template fingerprint for topic %s: %v", topicID, err)
			continue
		}

		suggestion := map[string]interface{}{
			"stage":       stage.Stage,
			"fingerprint": fingerprint,
			"task_ids":    stage.Tasks,
			"task_types":  meta["task_types"],
			"occurrences": state.Occurrences,
			"threshold":   templateSuggestionThreshold,
		}
		status := "candidate"
		existingTemplate := state.TemplateID != ""
		templateID := state.TemplateID

		if !existingTemplate && state.Occurrences >= templateSuggestionThreshold {
			name := buildTemplateName(stage.Stage, meta)
			description := fmt.Sprintf("Auto-suggested template for stage %s covering %d tasks", stage.Stage, len(stage.Tasks))
			template, err := p.templates.CreateTemplate(ctx, TemplateCreationRequest{
				TopicID:     topicID,
				Name:        name,
				Description: description,
				CreatedBy:   "system",
				Fingerprint: fingerprint,
				Graph:       graphJSON,
				Parameters:  paramsJSON,
				Metadata:    meta,
				Changelog:   fmt.Sprintf("Auto-generated from stage %s fingerprint %s", stage.Stage, fingerprint[:8]),
			})
			if err != nil {
				p.logger.Printf("warn: create procedural template suggestion: %v", err)
			} else {
				templateID = template.ID
				status = templateStatusPendingApproval
				suggestion["template_id"] = template.ID
				suggestion["template_version"] = template.Version.Version
				suggestion["status"] = status
				if err := p.templates.LinkFingerprint(ctx, topicID, fingerprint, template.ID); err != nil {
					p.logger.Printf("warn: link fingerprint %s to template %s: %v", fingerprint, template.ID, err)
				}
			}
		}

		if templateID != "" && existingTemplate {
			status = "reused"
			suggestion["template_id"] = templateID
			suggestion["status"] = status
			usageMeta := map[string]interface{}{
				"stage":       stage.Stage,
				"occurrences": state.Occurrences,
			}
			if types, ok := meta["task_types"]; ok {
				usageMeta["task_types"] = types
			}
			if err := p.templates.RecordUsage(ctx, TemplateUsageRecord{
				TopicID:     topicID,
				TemplateID:  templateID,
				Fingerprint: fingerprint,
				Stage:       stage.Stage,
				Success:     true,
				Metadata:    usageMeta,
			}); err != nil {
				p.logger.Printf("warn: record template usage %s: %v", templateID, err)
			}
		} else if _, ok := suggestion["status"]; !ok {
			suggestion["status"] = status
		}

		suggestions = append(suggestions, suggestion)
		if p.telemetry != nil {
			p.telemetry.RecordTemplateEvent(ctx, telemetry.TemplateEvent{
				TopicID:     topicID,
				Stage:       stage.Stage,
				Fingerprint: fingerprint,
				TemplateID:  templateID,
				Occurrences: state.Occurrences,
				TaskCount:   len(stage.Tasks),
				Status:      suggestion["status"].(string),
			})
		}
	}

	if len(suggestions) == 0 {
		return
	}
	if plan.Graph.Metadata == nil {
		plan.Graph.Metadata = make(map[string]interface{})
	}
	plan.Graph.Metadata["procedural_templates"] = suggestions
	if raw, err := json.Marshal(plan.Graph); err == nil {
		plan.RawJSON = raw
	} else if p.logger != nil {
		p.logger.Printf("warn: serialise plan with procedural templates: %v", err)
	}
}

type stageTaskSnapshot struct {
	ID            string   `json:"id"`
	Type          string   `json:"type"`
	DependsOn     []string `json:"depends_on,omitempty"`
	Outputs       []string `json:"outputs,omitempty"`
	ParameterKeys []string `json:"parameter_keys,omitempty"`
}

type stageSnapshot struct {
	Stage string              `json:"stage"`
	Tasks []stageTaskSnapshot `json:"tasks"`
}

func computeStageFingerprint(stage plannerv1.PlanStage, tasks map[string]plannerv1.PlanTask) (string, json.RawMessage, json.RawMessage, map[string]interface{}, error) {
	stageTaskSet := make(map[string]struct{}, len(stage.Tasks))
	for _, id := range stage.Tasks {
		stageTaskSet[id] = struct{}{}
	}
	snapshot := stageSnapshot{Stage: stage.Stage}
	paramSummary := make(map[string][]string)
	typeSet := make(map[string]struct{})

	for _, id := range stage.Tasks {
		task, ok := tasks[id]
		if !ok {
			continue
		}
		typeSet[task.Type] = struct{}{}
		var depends []string
		for _, dep := range task.DependsOn {
			if _, ok := stageTaskSet[dep]; ok {
				depends = append(depends, dep)
			}
		}
		sort.Strings(depends)
		outputs := append([]string(nil), task.Outputs...)
		sort.Strings(outputs)
		var paramKeys []string
		if len(task.Parameters) > 0 {
			for key := range task.Parameters {
				paramKeys = append(paramKeys, key)
			}
			sort.Strings(paramKeys)
			paramSummary[id] = append([]string(nil), paramKeys...)
		}
		snapshot.Tasks = append(snapshot.Tasks, stageTaskSnapshot{
			ID:            task.ID,
			Type:          task.Type,
			DependsOn:     depends,
			Outputs:       outputs,
			ParameterKeys: paramKeys,
		})
	}

	if len(snapshot.Tasks) == 0 {
		return "", nil, nil, nil, fmt.Errorf("stage %s contains no recognised tasks", stage.Stage)
	}
	sort.Slice(snapshot.Tasks, func(i, j int) bool {
		return snapshot.Tasks[i].ID < snapshot.Tasks[j].ID
	})

	graphBytes, err := json.Marshal(snapshot)
	if err != nil {
		return "", nil, nil, nil, err
	}
	sum := sha256.Sum256(graphBytes)
	fingerprint := hex.EncodeToString(sum[:])

	paramsBytes, err := json.Marshal(paramSummary)
	if err != nil {
		return "", nil, nil, nil, err
	}

	typeList := make([]string, 0, len(typeSet))
	for typ := range typeSet {
		typeList = append(typeList, typ)
	}
	sort.Strings(typeList)
	metadata := map[string]interface{}{
		"stage":      stage.Stage,
		"task_types": typeList,
		"task_count": len(snapshot.Tasks),
	}

	return fingerprint, graphBytes, paramsBytes, metadata, nil
}

func buildTemplateName(stage string, meta map[string]interface{}) string {
	nameParts := []string{strings.ReplaceAll(stage, "_", " ")}
	if raw, ok := meta["task_types"].([]string); ok && len(raw) > 0 {
		nameParts = append(nameParts, strings.Join(raw, "+"))
	}
	name := strings.TrimSpace(strings.Title(strings.Join(nameParts, " ")))
	if name == "" {
		name = "Procedural Template"
	}
	return name
}
