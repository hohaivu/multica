ALTER TABLE vcs_connection
    ADD COLUMN IF NOT EXISTS auth_kind TEXT NOT NULL DEFAULT 'pat' CHECK (auth_kind IN ('pat', 'oauth')),
    ADD COLUMN IF NOT EXISTS refresh_token_encrypted TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS access_token_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS credential_status TEXT NOT NULL DEFAULT 'ok' CHECK (credential_status IN ('ok', 'expired'));

CREATE TABLE IF NOT EXISTS vcs_webhook_registration (
    connection_id UUID NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('project', 'group')),
    target_id BIGINT NOT NULL,
    target_path TEXT NOT NULL,
    hook_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (connection_id, scope, target_id)
);
