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

-- name: DeleteRotaAssignment :exec
DELETE FROM rota_assignments
WHERE id = ?;

-- name: GetAssignmentByID :one
SELECT id, date, member_id, is_cover, original_assignment_id
FROM rota_assignments
WHERE id = ?;