DROP TRIGGER IF EXISTS user_scheduling_settings_updated_at ON user_scheduling_settings;
DROP TRIGGER IF EXISTS scheduling_inbox_updated_at ON scheduling_inbox;

DROP TABLE IF EXISTS user_scheduling_settings;
DROP INDEX IF EXISTS idx_calendar_objects_schedule_tag;
ALTER TABLE calendar_objects DROP COLUMN IF EXISTS schedule_tag;

DROP INDEX IF EXISTS idx_scheduling_inbox_received_at;
DROP INDEX IF EXISTS idx_scheduling_inbox_processed;
DROP INDEX IF EXISTS idx_scheduling_inbox_user_id;
DROP TABLE IF EXISTS scheduling_inbox;

ALTER TABLE calendars DROP COLUMN IF EXISTS schedule_transp;
