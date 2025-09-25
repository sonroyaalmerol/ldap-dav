-- Drop triggers
DROP TRIGGER IF EXISTS update_contacts_updated_at ON contacts;
DROP TRIGGER IF EXISTS update_addressbooks_updated_at ON addressbooks;
DROP FUNCTION IF EXISTS update_updated_at_column();

-- Drop indexes
DROP INDEX IF EXISTS idx_addressbook_changes_addressbook_seq;
DROP INDEX IF EXISTS idx_contacts_uid;
DROP INDEX IF EXISTS idx_contacts_addressbook_id;
DROP INDEX IF EXISTS idx_addressbooks_owner_group;
DROP INDEX IF EXISTS idx_addressbooks_owner_user_id;

-- Drop tables
DROP TABLE IF EXISTS addressbook_changes;
DROP TABLE IF EXISTS contacts;
DROP TABLE IF EXISTS addressbooks;
