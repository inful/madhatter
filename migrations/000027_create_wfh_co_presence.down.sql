-- SQLite does not support DROP TABLE in a single statement that
-- participates in foreign-key rollback chains. The simplest
-- rollback for a CREATE TABLE migration is DROP TABLE — the
-- table has no FK dependents yet, no row backfill is at risk,
-- and the indexes are dropped implicitly. If a future migration
-- adds FKs pointing at wfh_co_presence (none planned), this
-- script will need to use the rename-and-rebuild recipe.
DROP TABLE IF EXISTS wfh_co_presence;
