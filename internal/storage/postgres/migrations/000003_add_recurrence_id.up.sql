-- Add recurrence_id column (nullable for backwards compatibility)
ALTER TABLE calendar_objects 
ADD COLUMN IF NOT EXISTS recurrence_id TEXT;

-- Populate recurrence_id from existing data
UPDATE calendar_objects 
SET recurrence_id = (
  SELECT substring(data FROM 'RECURRENCE-ID[^:]*:([^\r\n]+)')
)
WHERE data LIKE '%RECURRENCE-ID%' AND recurrence_id IS NULL;

-- Drop old unique index
DROP INDEX IF EXISTS objects_calendar_uid_unique;

-- Create new composite unique index (null recurrence_id represents master event)
CREATE UNIQUE INDEX IF NOT EXISTS objects_calendar_uid_recurrence_unique
  ON calendar_objects(calendar_id, uid, COALESCE(recurrence_id, ''));

-- Add index for recurrence_id lookups
CREATE INDEX IF NOT EXISTS idx_objects_recurrence_id
  ON calendar_objects(calendar_id, uid, recurrence_id) 
  WHERE recurrence_id IS NOT NULL;
