-- SQLite doesn't support DROP COLUMN easily, so we recreate the table
CREATE TABLE calendar_objects_new (
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

INSERT INTO calendar_objects_new 
SELECT id, calendar_id, uid, etag, data, component, start_at, end_at, updated_at
FROM calendar_objects;

DROP TABLE calendar_objects;
ALTER TABLE calendar_objects_new RENAME TO calendar_objects;

CREATE UNIQUE INDEX objects_calendar_uid_unique
    ON calendar_objects(calendar_id, uid);

CREATE INDEX idx_objects_cal_comp_time
    ON calendar_objects(calendar_id, component, start_at, end_at);

CREATE INDEX idx_objects_cal_updated
    ON calendar_objects(calendar_id, updated_at);
