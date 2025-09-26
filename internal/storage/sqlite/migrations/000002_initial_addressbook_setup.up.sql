-- Create addressbooks table
CREATE TABLE IF NOT EXISTS addressbooks (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT,
    owner_group TEXT,
    uri TEXT NOT NULL,
    display_name TEXT,
    description TEXT,
    ctag TEXT NOT NULL,
    sync_seq INTEGER NOT NULL DEFAULT 0,
    sync_token TEXT NOT NULL DEFAULT 'seq:0',
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS addressbooks_owner_uri_unique
    ON addressbooks(owner_user_id, uri);

-- Create contacts table
CREATE TABLE IF NOT EXISTS contacts (
    id TEXT PRIMARY KEY,
    addressbook_id TEXT NOT NULL,
    uid TEXT NOT NULL,
    etag TEXT NOT NULL,
    data TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (addressbook_id) REFERENCES addressbooks(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS contacts_addressbook_uid_unique
    ON contacts(addressbook_id, uid);

-- Create addressbook_changes table for sync
CREATE TABLE IF NOT EXISTS addressbook_changes (
    addressbook_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    uid TEXT NOT NULL,
    deleted BOOLEAN NOT NULL DEFAULT 0,
    changed_at DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (addressbook_id, seq),
    FOREIGN KEY (addressbook_id) REFERENCES addressbooks(id) ON DELETE CASCADE
);

-- Create indexes
CREATE INDEX IF NOT EXISTS idx_addressbooks_owner_user_id ON addressbooks(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_addressbooks_owner_group ON addressbooks(owner_group);
CREATE INDEX IF NOT EXISTS idx_contacts_addressbook_id ON contacts(addressbook_id);
CREATE INDEX IF NOT EXISTS idx_contacts_uid ON contacts(uid);
CREATE INDEX IF NOT EXISTS idx_addressbook_changes_addressbook_seq ON addressbook_changes(addressbook_id, seq);
CREATE INDEX IF NOT EXISTS idx_contacts_addressbook_updated ON contacts(addressbook_id, updated_at);

-- Create triggers for automatic updated_at
CREATE TRIGGER IF NOT EXISTS update_addressbooks_updated_at
    AFTER UPDATE ON addressbooks
BEGIN
    UPDATE addressbooks SET updated_at = datetime('now') WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS update_contacts_updated_at
    AFTER UPDATE ON contacts
BEGIN
    UPDATE contacts SET updated_at = datetime('now') WHERE id = NEW.id;
END;
