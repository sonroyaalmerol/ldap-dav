-- Drop new indexes
DROP INDEX IF EXISTS objects_calendar_uid_recurrence_unique;
DROP INDEX IF EXISTS idx_objects_recurrence_id;

-- Recreate old unique index
CREATE UNIQUE INDEX IF NOT EXISTS objects_calendar_uid_unique
  ON calendar_objects(calendar_id, uid);

-- Remove recurrence_id column
ALTER TABLE calendar_objects 
DROP COLUMN IF EXISTS recurrence_id;
