-- Read-only assistant tools run immediately and do not pretend to be confirmed.
-- +goose Up
ALTER TABLE assistant_tool_executions
    DROP CONSTRAINT assistant_tool_executions_lifecycle_consistent;
ALTER TABLE assistant_tool_executions
    ADD CONSTRAINT assistant_tool_executions_lifecycle_consistent CHECK (
        (status = 'proposed' AND consequence IN ('write', 'destructive')
            AND confirmed_at IS NULL AND completed_at IS NULL)
        OR (status = 'running' AND completed_at IS NULL AND (
            (consequence = 'read' AND confirmed_at IS NULL)
            OR (consequence IN ('write', 'destructive') AND confirmed_at IS NOT NULL)
        ))
        OR (status IN ('succeeded', 'failed') AND completed_at IS NOT NULL AND (
            (consequence = 'read' AND confirmed_at IS NULL)
            OR (consequence IN ('write', 'destructive') AND confirmed_at IS NOT NULL)
        ))
        OR (status = 'rejected' AND consequence IN ('write', 'destructive')
            AND completed_at IS NOT NULL)
    );

-- +goose Down
ALTER TABLE assistant_tool_executions
    DROP CONSTRAINT assistant_tool_executions_lifecycle_consistent;
ALTER TABLE assistant_tool_executions
    ADD CONSTRAINT assistant_tool_executions_lifecycle_consistent CHECK (
        (status = 'proposed' AND confirmed_at IS NULL AND completed_at IS NULL)
        OR (status = 'running' AND confirmed_at IS NOT NULL AND completed_at IS NULL)
        OR (status IN ('succeeded', 'failed')
            AND confirmed_at IS NOT NULL AND completed_at IS NOT NULL)
        OR (status = 'rejected' AND completed_at IS NOT NULL)
    ) NOT VALID;
