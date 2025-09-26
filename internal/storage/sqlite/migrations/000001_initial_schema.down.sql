-- Drop triggers first
DROP TRIGGER IF EXISTS update_calendar_objects_updated_at;
DROP TRIGGER IF EXISTS update_calendars_updated_at;

-- Drop indexes
DROP INDEX IF EXISTS idx_objects_cal_updated;
DROP INDEX IF EXISTS idx_changes_cal_seq;
DROP INDEX IF EXISTS idx_objects_cal_comp_time;
DROP INDEX IF EXISTS calendar_changes_calendar_seq_idx;
DROP INDEX IF EXISTS objects_calendar_uid_unique;
DROP INDEX IF EXISTS calendars_owner_uri_unique;

-- Drop tables in reverse dependency order
DROP TABLE IF EXISTS calendar_changes;
DROP TABLE IF EXISTS calendar_objects;
DROP TABLE IF EXISTS calendars;
