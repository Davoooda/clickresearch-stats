-- Create funnels table for storing user-defined funnel configurations
-- Note: "window" is a reserved word in PostgreSQL, so we use "funnel_window"
CREATE TABLE IF NOT EXISTS clickresearch_funnels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES clickresearch_projects(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    funnel_window INTEGER NOT NULL DEFAULT 60,
    steps JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Index for fast lookup by project
CREATE INDEX IF NOT EXISTS idx_funnels_project_id ON clickresearch_funnels(project_id);

-- Index for ordering by created_at
CREATE INDEX IF NOT EXISTS idx_funnels_created_at ON clickresearch_funnels(created_at DESC);
