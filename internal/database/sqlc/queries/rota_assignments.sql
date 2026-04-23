-- name: CreateRotaAssignment :execresult
INSERT INTO rota_assignments (id, date, member_id, is_cover, original_assignment_id)
VALUES (?, ?, ?, ?, ?);

-- name: GetAssignmentsByDate :many
SELECT ra.id, ra.date, ra.member_id, ra.is_cover, ra.original_assignment_id, tm.name AS member_name, tm.email AS member_email
FROM rota_assignments ra
JOIN team_members tm ON ra.member_id = tm.id
WHERE ra.date = ?;

-- name: GetUpcomingAssignments :many
SELECT id, date, member_id, is_cover, original_assignment_id
FROM rota_assignments
WHERE member_id = ? AND date >= date('now') AND date <= date('now', '+' || ? || ' days')
ORDER BY date;

-- name: GetAssignmentsByDateRange :many
SELECT ra.id, ra.date, ra.member_id, ra.is_cover, ra.original_assignment_id, tm.name AS member_name, tm.email AS member_email
FROM rota_assignments ra
JOIN team_members tm ON ra.member_id = tm.id
WHERE ra.date >= ? AND ra.date <= ?
ORDER BY ra.date;

-- name: GetMostRecentCoverAssignment :one
SELECT id, date, member_id
FROM rota_assignments
WHERE is_cover = 1
ORDER BY date DESC, created_at DESC
LIMIT 1;

-- name: DeleteRotaAssignment :exec
DELETE FROM rota_assignments
WHERE id = ?;

-- name: GetAssignmentByID :one
SELECT id, date, member_id, is_cover, original_assignment_id
FROM rota_assignments
WHERE id = ?;

-- name: GetLatestAssignmentDate :one
SELECT MAX(date) AS max_date
FROM rota_assignments;

-- name: DeleteAssignmentsByDateRange :exec
DELETE FROM rota_assignments
WHERE date >= ? AND date <= ?;

-- name: UpdateCoverMember :exec
UPDATE rota_assignments
SET member_id = ?
WHERE date = ? AND is_cover = 1;

-- name: UpdateAssignmentMember :exec
UPDATE rota_assignments
SET member_id = ?
WHERE id = ?;

-- name: GetFutureAssignmentsForMember :many
SELECT ra.id, ra.date, ra.member_id, ra.is_cover, ra.original_assignment_id,
       tm.name AS member_name, tm.email AS member_email
FROM rota_assignments ra
JOIN team_members tm ON ra.member_id = tm.id
WHERE ra.member_id = ? AND ra.date >= date('now')
ORDER BY ra.date;

-- name: GetFutureAssignments :many
SELECT ra.id, ra.date, ra.member_id, ra.is_cover, ra.original_assignment_id,
       tm.name AS member_name, tm.email AS member_email
FROM rota_assignments ra
JOIN team_members tm ON ra.member_id = tm.id
WHERE ra.date >= date('now')
ORDER BY ra.date;

-- name: GetCoverAssignmentByDate :one
SELECT id, date, member_id, is_cover, original_assignment_id
FROM rota_assignments
WHERE date = ? AND is_cover = 1;