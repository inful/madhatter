-- name: RecordWFHCoPresencePair :execresult
INSERT OR IGNORE INTO wfh_co_presence (co_presence_id, working_date, member_id_a, member_id_b)
VALUES (?, ?, ?, ?);

-- name: PruneCoPresenceOlderThan :execresult
DELETE FROM wfh_co_presence WHERE julianday(working_date) < julianday(?);

-- name: GetLatestCoPresenceWithCohortA :many
SELECT working_date
FROM wfh_co_presence
WHERE julianday(working_date) >= julianday(?)
  AND julianday(working_date) < julianday(?)
  AND member_id_a = ?
  AND member_id_b IN (?, ?, ?)
ORDER BY julianday(working_date) DESC
LIMIT 1;

-- name: GetLatestCoPresenceWithCohortB :many
SELECT working_date
FROM wfh_co_presence
WHERE julianday(working_date) >= julianday(?)
  AND julianday(working_date) < julianday(?)
  AND member_id_b = ?
  AND member_id_a IN (?, ?, ?)
ORDER BY julianday(working_date) DESC
LIMIT 1;
