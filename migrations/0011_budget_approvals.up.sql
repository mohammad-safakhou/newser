CREATE TABLE IF NOT EXISTS run_budget_approvals (
    run_id UUID PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
    topic_id UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    estimated_cost NUMERIC(12,4) NOT NULL,
    approval_threshold NUMERIC(12,4) NOT NULL,
    requested_by UUID NOT NULL REFERENCES users(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at TIMESTAMPTZ,
    decided_by UUID REFERENCES users(id),
    reason TEXT
);
