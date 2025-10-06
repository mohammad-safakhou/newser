package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/labstack/echo/v4"
	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/store"
)

func TestBuilderSaveSchema(t *testing.T) {
	e := echo.New()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	handler := NewBuilderHandler(&config.Config{}, &store.Store{DB: db})

	mock.ExpectQuery(`SELECT name, preferences, schedule_cron FROM topics WHERE id=\$1 AND user_id=\$2`).
		WithArgs("topic", "user").
		WillReturnRows(sqlmock.NewRows([]string{"name", "preferences", "schedule_cron"}).AddRow("Topic", []byte(`{}`), "@daily"))

	mock.ExpectQuery(`WITH next_version AS \(
    SELECT COALESCE\(MAX\(version\), 0\) \+ 1 AS version
    FROM builder_schemas
    WHERE topic_id = \$1 AND kind = \$2
\)
INSERT INTO builder_schemas \(id, topic_id, kind, version, content, author_id\)
SELECT gen_random_uuid\(\), \$1, \$2, version, \$3, \$4 FROM next_version
RETURNING id, topic_id, kind, version, content, author_id, created_at`).
		WithArgs("topic", "topic", []byte(`{"name":"example"}`), nil).
		WillReturnRows(sqlmock.NewRows([]string{"id", "topic_id", "kind", "version", "content", "author_id", "created_at"}).
			AddRow("schema-1", "topic", "topic", 1, []byte(`{"name":"example"}`), nil, time.Now()))

	req := httptest.NewRequest(http.MethodPost, "/api/topics/topic/builder/schema", strings.NewReader(`{"kind":"topic","content":{"name":"example"}}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("topic_id")
	ctx.SetParamValues("topic")
	ctx.Set("user_id", "user")

	if err := handler.saveSchema(ctx); err != nil {
		t.Fatalf("saveSchema: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestBuilderDiff(t *testing.T) {
	e := echo.New()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	handler := NewBuilderHandler(&config.Config{}, &store.Store{DB: db})

	mock.ExpectQuery(`SELECT name, preferences, schedule_cron FROM topics WHERE id=\$1 AND user_id=\$2`).
		WithArgs("topic", "user").
		WillReturnRows(sqlmock.NewRows([]string{"name", "preferences", "schedule_cron"}).AddRow("Topic", []byte(`{}`), "@daily"))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, topic_id, kind, version, content, author_id, created_at
FROM builder_schemas
WHERE topic_id=$1 AND kind=$2 AND version=$3
`)).
		WithArgs("topic", "topic", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "topic_id", "kind", "version", "content", "author_id", "created_at"}).
			AddRow("schema-1", "topic", "topic", 1, []byte(`{"name":"v1"}`), nil, time.Now()))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, topic_id, kind, version, content, author_id, created_at
FROM builder_schemas
WHERE topic_id=$1 AND kind=$2 AND version=$3
`)).
		WithArgs("topic", "topic", 2).
		WillReturnRows(sqlmock.NewRows([]string{"id", "topic_id", "kind", "version", "content", "author_id", "created_at"}).
			AddRow("schema-2", "topic", "topic", 2, []byte(`{"name":"v2"}`), nil, time.Now()))

	req := httptest.NewRequest(http.MethodGet, "/api/topics/topic/builder/diff?kind=topic&from=1&to=2", nil)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("topic_id")
	ctx.SetParamValues("topic")
	ctx.Set("user_id", "user")

	if err := handler.diff(ctx); err != nil {
		t.Fatalf("diff: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp builderDiffResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.FromVersion != 1 || resp.ToVersion != 2 {
		t.Fatalf("unexpected diff response: %+v", resp)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestBuilderGetLatestDefault(t *testing.T) {
	e := echo.New()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	handler := NewBuilderHandler(&config.Config{}, &store.Store{DB: db})

	mock.ExpectQuery(`SELECT name, preferences, schedule_cron FROM topics WHERE id=\$1 AND user_id=\$2`).
		WithArgs("topic", "user").
		WillReturnRows(sqlmock.NewRows([]string{"name", "preferences", "schedule_cron"}).AddRow("Topic", []byte(`{}`), "@daily"))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, topic_id, kind, version, content, author_id, created_at
FROM builder_schemas
WHERE topic_id=$1 AND kind=$2
ORDER BY version DESC
LIMIT 1
`)).
		WithArgs("topic", "topic").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	req := httptest.NewRequest(http.MethodGet, "/api/topics/topic/builder/schema", nil)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("topic_id")
	ctx.SetParamValues("topic")
	ctx.Set("user_id", "user")

	if err := handler.getLatest(ctx); err != nil {
		t.Fatalf("getLatest: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Daily Brief") {
		t.Fatalf("expected default topic schema, got %s", rec.Body.String())
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
