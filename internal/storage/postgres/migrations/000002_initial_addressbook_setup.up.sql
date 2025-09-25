-- Create addressbooks table
CREATE TABLE IF NOT EXISTS addressbooks (
    id UUID PRIMARY KEY,
    owner_user_id TEXT,
    owner_group TEXT,
    uri TEXT NOT NULL,
    display_name TEXT,
    description TEXT,
    ctag TEXT NOT NULL,
    sync_seq BIGINT NOT NULL DEFAULT 0,
    sync_token TEXT NOT NULL DEFAULT 'seq:0',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS addressbooks_owner_uri_unique
    ON addressbooks(owner_user_id, uri);

-- Create contacts table
CREATE TABLE IF NOT EXISTS contacts (
    id UUID PRIMARY KEY,
    addressbook_id UUID NOT NULL REFERENCES addressbooks(id) ON DELETE CASCADE,
    uid TEXT NOT NULL,
    etag TEXT NOT NULL,
    data TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS contacts_addressbook_uid_unique
    ON contacts(addressbook_id, uid);

-- Create addressbook_changes table for sync
CREATE TABLE IF NOT EXISTS addressbook_changes (
    addressbook_id UUID NOT NULL REFERENCES addressbooks(id) ON DELETE CASCADE,
    seq BIGINT NOT NULL,
    uid TEXT NOT NULL,
    deleted BOOLEAN NOT NULL DEFAULT FALSE,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (addressbook_id, seq)
);

-- Create indexes
CREATE INDEX IF NOT EXISTS idx_addressbooks_owner_user_id ON addressbooks(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_addressbooks_owner_group ON addressbooks(owner_group);
CREATE INDEX IF NOT EXISTS idx_contacts_addressbook_id ON contacts(addressbook_id);
CREATE INDEX IF NOT EXISTS idx_contacts_uid ON contacts(uid);
CREATE INDEX IF NOT EXISTS idx_addressbook_changes_addressbook_seq ON addressbook_changes(addressbook_id, seq);
CREATE INDEX IF NOT EXISTS idx_contacts_addressbook_updated ON contacts(addressbook_id, updated_at);
