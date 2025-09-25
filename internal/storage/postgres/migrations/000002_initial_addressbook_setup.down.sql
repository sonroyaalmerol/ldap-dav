-- Drop triggers first
DROP TRIGGER IF EXISTS update_addressbooks_updated_at ON addressbooks;
DROP TRIGGER IF EXISTS update_contacts_updated_at ON contacts;
DROP FUNCTION IF EXISTS update_updated_at_column();

-- Drop indexes
DROP INDEX IF EXISTS idx_contacts_addressbook_updated;
DROP INDEX IF EXISTS idx_addressbook_changes_addressbook_seq;
DROP INDEX IF EXISTS idx_contacts_uid;
DROP INDEX IF EXISTS idx_contacts_addressbook_id;
DROP INDEX IF EXISTS idx_addressbooks_owner_group;
DROP INDEX IF EXISTS idx_addressbooks_owner_user_id;
DROP INDEX IF EXISTS contacts_addressbook_uid_unique;
DROP INDEX IF EXISTS addressbooks_owner_uri_unique;

-- Drop tables in reverse dependency order
DROP TABLE IF EXISTS addressbook_changes;
DROP TABLE IF EXISTS contacts;
DROP TABLE IF EXISTS addressbooks;
