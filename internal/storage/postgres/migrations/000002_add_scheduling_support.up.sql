-- Create the updated_at trigger function
CREATE OR REPLACE FUNCTION updated_at_trigger()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Scheduling inbox messages table
CREATE TABLE scheduling_inbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL,
    uid TEXT NOT NULL,
    method TEXT NOT NULL, -- REQUEST, REPLY, CANCEL, etc.
    data TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_scheduling_inbox_user_id ON scheduling_inbox(user_id);
CREATE INDEX idx_scheduling_inbox_processed ON scheduling_inbox(processed);
CREATE INDEX idx_scheduling_inbox_received_at ON scheduling_inbox(received_at);

-- Schedule tags for calendar objects (for conflict resolution)
ALTER TABLE calendar_objects ADD COLUMN schedule_tag TEXT;
CREATE INDEX idx_calendar_objects_schedule_tag ON calendar_objects(schedule_tag);

-- Default calendar for incoming scheduling messages per user
CREATE TABLE user_scheduling_settings (
    user_id TEXT PRIMARY KEY,
    default_calendar_id UUID REFERENCES calendars(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Calendar transparency settings (for free/busy calculations)
ALTER TABLE calendars ADD COLUMN schedule_transp TEXT DEFAULT 'opaque';

-- Update triggers
CREATE TRIGGER scheduling_inbox_updated_at BEFORE UPDATE ON scheduling_inbox 
    FOR EACH ROW EXECUTE FUNCTION updated_at_trigger();

CREATE TRIGGER user_scheduling_settings_updated_at BEFORE UPDATE ON user_scheduling_settings 
    FOR EACH ROW EXECUTE FUNCTION updated_at_trigger();
