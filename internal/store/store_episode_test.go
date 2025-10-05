package store

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	core "github.com/mohammad-safakhou/newser/internal/agent/core"
	"github.com/mohammad-safakhou/newser/internal/planner"
)

func TestSaveEpisode(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}

	now := time.Now()
	ep := Episode{
		RunID:   "run-1",
		TopicID: "topic-1",
		UserID:  "user-1",
		Thought: core.UserThought{ID: "run-1", Content: "hello"},
		PlanDocument: &planner.PlanDocument{
			Version: "v1",
			Tasks:   []planner.PlanTask{{ID: "task-1", Type: "research"}},
		},
		PlanRaw:    json.RawMessage(`{"version":"v1"}`),
		PlanPrompt: "plan prompt",
		Result:     core.ProcessingResult{ID: "run-1", Summary: "summary"},
		Steps: []EpisodeStep{
			{
				StepIndex:     1,
				Task:          core.AgentTask{ID: "task-1", Type: "research"},
				InputSnapshot: map[string]interface{}{"query": "foo"},
				Prompt:        "prompt",
				Result:        core.AgentResult{ID: "task-1_result", TaskID: "task-1", AgentType: "research", Success: true},
			},
		},
	}

	mock.ExpectBegin()

	insertEpisodeQuery := regexp.QuoteMeta(`
INSERT INTO run_episodes (run_id, topic_id, user_id, thought, plan_document, plan_raw, plan_prompt, result)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (run_id) DO UPDATE SET
  topic_id = EXCLUDED.topic_id,
  user_id = EXCLUDED.user_id,
  thought = EXCLUDED.thought,
  plan_document = EXCLUDED.plan_document,
  plan_raw = EXCLUDED.plan_raw,
  plan_prompt = EXCLUDED.plan_prompt,
  result = EXCLUDED.result
RETURNING id, created_at;
`)
	mock.ExpectQuery(insertEpisodeQuery).
		WithArgs(ep.RunID, ep.TopicID, ep.UserID, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), ep.PlanPrompt, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow("episode-1", now))

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM run_episode_steps WHERE episode_id=$1`)).
		WithArgs("episode-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	insertStepQuery := regexp.QuoteMeta(`
INSERT INTO run_episode_steps (episode_id, step_index, task, input_snapshot, prompt, result, artifacts, started_at, completed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
`)
	mock.ExpectExec(insertStepQuery).
		WithArgs("episode-1", 1, sqlmock.AnyArg(), sqlmock.AnyArg(), ep.Steps[0].Prompt, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectCommit()

	if err := st.SaveEpisode(context.Background(), ep); err != nil {
		t.Fatalf("SaveEpisode: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestGetEpisodeByRunID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}

	now := time.Now()
	thoughtBytes, _ := json.Marshal(core.UserThought{ID: "run-1", Content: "hello"})
	planDocBytes, _ := json.Marshal(planner.PlanDocument{Version: "v1"})
	resultBytes, _ := json.Marshal(core.ProcessingResult{ID: "run-1", Summary: "summary"})
	stepTaskBytes, _ := json.Marshal(core.AgentTask{ID: "task-1"})
	stepResultBytes, _ := json.Marshal(core.AgentResult{ID: "task-1_result", TaskID: "task-1"})

	mock.ExpectQuery(regexp.QuoteMeta(`
SELECT id, run_id, topic_id, user_id, thought, plan_document, plan_raw, plan_prompt, result, created_at
FROM run_episodes
WHERE run_id=$1
`)).
		WithArgs("run-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "run_id", "topic_id", "user_id", "thought", "plan_document", "plan_raw", "plan_prompt", "result", "created_at"}).
			AddRow("episode-1", "run-1", "topic-1", "user-1", thoughtBytes, planDocBytes, planDocBytes, "plan prompt", resultBytes, now))

	mock.ExpectQuery(regexp.QuoteMeta(`
SELECT step_index, task, input_snapshot, prompt, result, artifacts, started_at, completed_at, created_at
FROM run_episode_steps
WHERE episode_id=$1
ORDER BY step_index
`)).
		WithArgs("episode-1").
		WillReturnRows(sqlmock.NewRows([]string{"step_index", "task", "input_snapshot", "prompt", "result", "artifacts", "started_at", "completed_at", "created_at"}).
			AddRow(1, stepTaskBytes, nil, "prompt", stepResultBytes, nil, now, now, now))

	ep, ok, err := st.GetEpisodeByRunID(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("GetEpisodeByRunID: %v", err)
	}
	if !ok {
		t.Fatalf("expected episode")
	}
	if ep.RunID != "run-1" || len(ep.Steps) != 1 || ep.Steps[0].Prompt != "prompt" {
		t.Fatalf("unexpected episode data: %+v", ep)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPruneEpisodesBefore(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	st := &Store{DB: db}

	cutoff := time.Now().Add(-24 * time.Hour)

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM run_episodes WHERE created_at < $1`)).
		WithArgs(cutoff).
		WillReturnResult(sqlmock.NewResult(0, 3))

	deleted, err := st.PruneEpisodesBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("PruneEpisodesBefore: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("expected 3 deletions, got %d", deleted)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
