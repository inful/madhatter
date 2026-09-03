-- name: RecordWFHCoPresencePair :execresult
INSERT OR IGNORE INTO wfh_co_presence (co_presence_id, working_date, member_id_a, member_id_b)
VALUES (?, ?, ?, ?);

-- name: PruneCoPresenceOlderThan :execresult
DELETE FROM wfh_co_presence WHERE working_date < ?;

-- name: GetLatestCoPresenceWithCohortA :many
SELECT working_date
FROM wfh_co_presence
WHERE working_date >= ?
  AND working_date < ?
  AND member_id_a = ?
  AND member_id_b IN (?, ?, ?)
ORDER BY working_date DESC
LIMIT 1;

-- name: GetLatestCoPresenceWithCohortB :many
SELECT working_date
FROM wfh_co_presence
WHERE working_date >= ?
  AND working_date < ?
  AND member_id_b = ?
  AND member_id_a IN (?, ?, ?)
ORDER BY working_date DESC
LIMIT 1;
