DROP TABLE IF EXISTS vcs_webhook_registration;
ALTER TABLE vcs_connection
    DROP COLUMN IF EXISTS credential_status,
    DROP COLUMN IF EXISTS access_token_expires_at,
    DROP COLUMN IF EXISTS refresh_token_encrypted,
    DROP COLUMN IF EXISTS auth_kind;
