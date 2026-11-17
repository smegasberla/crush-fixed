-- name: GetMessage :one
SELECT *
FROM messages
WHERE id = ? LIMIT 1;

-- name: ListMessagesBySession :many
SELECT *
FROM messages
WHERE session_id = ?
ORDER BY created_at ASC;

-- name: ListMessagesBySessionFromSummary :many
-- Messages at or after the window marker, which is all a compacted session
-- sends. created_at has one-second resolution, so every message written in the
-- same second as the marker shares its timestamp; selecting by it would return
-- the whole transcript and the fold would never apply. rowid is monotonic, so
-- the marker's rowid is an exact lower bound.
SELECT m.*
FROM messages m
WHERE m.session_id = ?
  AND m.rowid >= (SELECT s.rowid FROM messages s WHERE s.id = ?)
ORDER BY m.rowid ASC;

-- name: CreateMessage :one
INSERT INTO messages (
    id,
    session_id,
    role,
    parts,
    model,
    model_name,
    provider,
    is_summary_message,
    created_at,
    updated_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, strftime('%s', 'now'), strftime('%s', 'now')
)
RETURNING *;

-- name: UpdateMessage :exec
UPDATE messages
SET
    parts = ?,
    model_name = ?,
    prism_model_id = ?,
    prism_model_name = ?,
    prism_hypercredit_savings = ?,
    prism_dollar_savings = ?,
    finished_at = ?,
    updated_at = strftime('%s', 'now')
WHERE id = ?;


-- name: DeleteMessage :exec
DELETE FROM messages
WHERE id = ?;

-- name: DeleteSessionMessages :exec
DELETE FROM messages
WHERE session_id = ?;

-- name: ListUserMessagesBySession :many
-- Backs prompt history, which steps back one entry at a time.
SELECT *
FROM messages
WHERE session_id = ? AND role = 'user'
ORDER BY created_at DESC
LIMIT 200;

-- name: ListAllUserMessages :many
-- Backs prompt history when no session is open. Needs
-- idx_messages_role_created_at to seek rather than scan the table.
-- Child sessions are excluded: a subagent's task prompt is a user
-- message too, but the user never typed it, so it has no place in the
-- prompt history.
SELECT m.*
FROM messages m
JOIN sessions s ON s.id = m.session_id
WHERE m.role = 'user' AND s.parent_session_id IS NULL
ORDER BY m.created_at DESC
LIMIT 200;

-- name: GetLastAssistantMessageBySession :one
SELECT *
FROM messages
WHERE session_id = ? AND role = 'assistant' AND is_summary_message = 0
ORDER BY created_at DESC
LIMIT 1;
