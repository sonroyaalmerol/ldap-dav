-- Create calendars table
CREATE TABLE IF NOT EXISTS calendars (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT,
    owner_group TEXT,
    uri TEXT NOT NULL,
    display_name TEXT,
    description TEXT,
    ctag TEXT NOT NULL,
    color TEXT DEFAULT '#3174ad',
    sync_seq INTEGER NOT NULL DEFAULT 0,
    sync_token TEXT NOT NULL DEFAULT 'seq:0',
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS calendars_owner_uri_unique
    ON calendars(owner_user_id, uri);

-- Create calendar_objects table
CREATE TABLE IF NOT EXISTS calendar_objects (
    id TEXT PRIMARY KEY,
    calendar_id TEXT NOT NULL,
    uid TEXT NOT NULL,
    etag TEXT NOT NULL,
    data TEXT NOT NULL,
    component TEXT NOT NULL,
    start_at DATETIME,
    end_at DATETIME,
    updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (calendar_id) REFERENCES calendars(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS objects_calendar_uid_unique
    ON calendar_objects(calendar_id, uid);

-- Create calendar_changes table
CREATE TABLE IF NOT EXISTS calendar_changes (
    calendar_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    uid TEXT NOT NULL,
    deleted BOOLEAN NOT NULL DEFAULT 0,
    changed_at DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (calendar_id, seq),
    FOREIGN KEY (calendar_id) REFERENCES calendars(id) ON DELETE CASCADE
);

-- Create indexes
CREATE INDEX IF NOT EXISTS calendar_changes_calendar_seq_idx
    ON calendar_changes(calendar_id, seq);

CREATE INDEX IF NOT EXISTS idx_objects_cal_comp_time
    ON calendar_objects(calendar_id, component, start_at, end_at);

CREATE INDEX IF NOT EXISTS idx_changes_cal_seq
    ON calendar_changes(calendar_id, seq);

CREATE INDEX IF NOT EXISTS idx_objects_cal_updated
    ON calendar_objects(calendar_id, updated_at);

-- Create triggers for automatic updated_at
CREATE TRIGGER IF NOT EXISTS update_calendars_updated_at
    AFTER UPDATE ON calendars
BEGIN
    UPDATE calendars SET updated_at = datetime('now') WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS update_calendar_objects_updated_at
    AFTER UPDATE ON calendar_objects
BEGIN
    UPDATE calendar_objects SET updated_at = datetime('now') WHERE id = NEW.id;
END;
