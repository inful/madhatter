-- name: AddTeamMember :execresult
INSERT INTO team_members (id, name, email)
VALUES (?, ?, ?);

-- name: GetActiveTeamMembers :many
SELECT id, name, email, is_active, is_permanent_wfh, is_exempt_from_assignment,
	   recurring_wfh_monday, recurring_wfh_tuesday, recurring_wfh_wednesday,
	   recurring_wfh_thursday, recurring_wfh_friday, created_at
FROM team_members
WHERE is_active = 1
ORDER BY name;

-- name: GetMemberByEmail :one
SELECT id, name, email, is_active, is_permanent_wfh, is_exempt_from_assignment,
	   recurring_wfh_monday, recurring_wfh_tuesday, recurring_wfh_wednesday,
	   recurring_wfh_thursday, recurring_wfh_friday, created_at
FROM team_members
WHERE email = ?;

-- name: GetMemberByID :one
SELECT id, name, email, is_active, is_permanent_wfh, is_exempt_from_assignment,
	   recurring_wfh_monday, recurring_wfh_tuesday, recurring_wfh_wednesday,
	   recurring_wfh_thursday, recurring_wfh_friday, created_at
FROM team_members
WHERE id = ?;

-- name: GetMemberByToken :one
SELECT tm.id, tm.name, tm.email, tm.is_active, tm.is_permanent_wfh, tm.is_exempt_from_assignment,
	   tm.recurring_wfh_monday, tm.recurring_wfh_tuesday, tm.recurring_wfh_wednesday,
	   tm.recurring_wfh_thursday, tm.recurring_wfh_friday, tm.created_at
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

-- name: SetTeamMemberExemptFromAssignment :exec
-- Toggle the seat-cap-picker exemption. Mirrors SetTeamMemberPermanentWFH
-- (also an :exec UPDATE) so the team-member-edit admin form can call
-- both setters side-by-side. The picker reads this flag in step 6.
UPDATE team_members
SET is_exempt_from_assignment = ?
WHERE id = ?;

-- name: SetTeamMemberRecurringWFHDays :exec
UPDATE team_members
SET recurring_wfh_monday = sqlc.arg(recurring_wfh_monday),
	recurring_wfh_tuesday = sqlc.arg(recurring_wfh_tuesday),
	recurring_wfh_wednesday = sqlc.arg(recurring_wfh_wednesday),
	recurring_wfh_thursday = sqlc.arg(recurring_wfh_thursday),
	recurring_wfh_friday = sqlc.arg(recurring_wfh_friday),
	is_permanent_wfh = CASE
		WHEN sqlc.arg(recurring_wfh_monday) = 1
		 AND sqlc.arg(recurring_wfh_tuesday) = 1
		 AND sqlc.arg(recurring_wfh_wednesday) = 1
		 AND sqlc.arg(recurring_wfh_thursday) = 1
		 AND sqlc.arg(recurring_wfh_friday) = 1 THEN 1
		ELSE 0
	END
WHERE id = sqlc.arg(id);

-- name: DeleteTeamMember :exec
DELETE FROM team_members
WHERE id = ?;