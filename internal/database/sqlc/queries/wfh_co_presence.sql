-- name: RecordWFHCoPresencePair :execresult
INSERT OR IGNORE INTO wfh_co_presence (co_presence_id, working_date, member_id_a, member_id_b)
VALUES (?, ?, ?, ?);

-- name: PruneCoPresenceOlderThan :execresult
DELETE FROM wfh_co_presence WHERE working_date < ?;
