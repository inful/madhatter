-- name: AddTeamMember :execresult
INSERT INTO team_members (id, name, email)
VALUES (?, ?, ?);

-- name: GetActiveTeamMembers :many
SELECT id, name, email, is_active, is_permanent_wfh, created_at
FROM team_members
WHERE is_active = 1
ORDER BY name;

-- name: GetMemberByEmail :one
SELECT id, name, email, is_active, is_permanent_wfh, created_at
FROM team_members
WHERE email = ?;

-- name: GetMemberByID :one
SELECT id, name, email, is_active, is_permanent_wfh, created_at
FROM team_members
WHERE id = ?;

-- name: GetMemberByToken :one
SELECT tm.id, tm.name, tm.email, tm.is_active, tm.is_permanent_wfh, tm.created_at
FROM calendar_subscriptions cs
JOIN team_members tm ON cs.member_id = tm.id
WHERE cs.token = ?;

-- name: DeactivateTeamMember :exec
UPDATE team_members
SET is_active = 0
WHERE id = ?;

-- name: ActivateTeamMember :exec
UPDATE team_members
SET is_active = 1
WHERE id = ?;

-- name: UpdateTeamMember :exec
UPDATE team_members
SET name = ?, email = ?
WHERE id = ?;

-- name: SetTeamMemberPermanentWFH :exec
UPDATE team_members
SET is_permanent_wfh = ?
WHERE id = ?;

-- name: DeleteTeamMember :exec
DELETE FROM team_members
WHERE id = ?;