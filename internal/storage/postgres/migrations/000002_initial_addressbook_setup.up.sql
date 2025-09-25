-- Create addressbooks table
CREATE TABLE IF NOT EXISTS addressbooks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id VARCHAR NOT NULL,
    owner_group VARCHAR NOT NULL,
    uri VARCHAR NOT NULL,
    display_name VARCHAR,
    description TEXT,
    color VARCHAR(7),
    ctag VARCHAR NOT NULL DEFAULT encode(gen_random_bytes(16), 'hex'),
    sync_token VARCHAR NOT NULL DEFAULT 'seq:0',
    sync_seq BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(owner_user_id, uri)
);

-- Create contacts table
CREATE TABLE IF NOT EXISTS contacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    addressbook_id UUID NOT NULL REFERENCES addressbooks(id) ON DELETE CASCADE,
    uid VARCHAR NOT NULL,
    etag VARCHAR NOT NULL DEFAULT encode(gen_random_bytes(16), 'hex'),
    data TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(addressbook_id, uid)
);

-- Create addressbook_changes table for sync
CREATE TABLE IF NOT EXISTS addressbook_changes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    addressbook_id UUID NOT NULL REFERENCES addressbooks(id) ON DELETE CASCADE,
    seq BIGINT NOT NULL,
    uid VARCHAR NOT NULL,
    deleted BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(addressbook_id, seq)
);

-- Create indexes
CREATE INDEX IF NOT EXISTS idx_addressbooks_owner_user_id ON addressbooks(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_addressbooks_owner_group ON addressbooks(owner_group);
CREATE INDEX IF NOT EXISTS idx_contacts_addressbook_id ON contacts(addressbook_id);
CREATE INDEX IF NOT EXISTS idx_contacts_uid ON contacts(uid);
CREATE INDEX IF NOT EXISTS idx_addressbook_changes_addressbook_seq ON addressbook_changes(addressbook_id, seq);

-- Create trigger to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_addressbooks_updated_at BEFORE UPDATE ON addressbooks
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_contacts_updated_at BEFORE UPDATE ON contacts
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
