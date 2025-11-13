-- Add recurrence_id column (nullable for backwards compatibility)
ALTER TABLE calendar_objects 
ADD COLUMN recurrence_id TEXT;

-- Populate recurrence_id from existing data (manual loop required in Go)
-- This will be handled in the Go code migration

-- Drop old unique index
DROP INDEX IF EXISTS objects_calendar_uid_unique;

-- Create new composite unique index (empty string represents null/master event)
CREATE UNIQUE INDEX IF NOT EXISTS objects_calendar_uid_recurrence_unique
  ON calendar_objects(calendar_id, uid, IFNULL(recurrence_id, ''));

-- Add index for recurrence_id lookups
CREATE INDEX IF NOT EXISTS idx_objects_recurrence_id
  ON calendar_objects(calendar_id, uid, recurrence_id) 
  WHERE recurrence_id IS NOT NULL;
