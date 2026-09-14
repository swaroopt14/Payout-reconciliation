-- +goose Up
ALTER TABLE connectors DROP CONSTRAINT IF EXISTS unique_provider_connector;
CREATE UNIQUE INDEX IF NOT EXISTS connectors_tenant_provider_connector_uidx
    ON connectors (tenant_id, provider, connector_id);

ALTER TABLE provider_webhook_receipts DROP CONSTRAINT IF EXISTS provider_webhook_receipts_connector_id_event_id_key;
CREATE UNIQUE INDEX IF NOT EXISTS provider_webhook_receipts_tenant_connector_event_uidx
    ON provider_webhook_receipts (tenant_id, connector_id, event_id);

ALTER TABLE auth_users DROP CONSTRAINT IF EXISTS auth_users_email_unique;
CREATE UNIQUE INDEX IF NOT EXISTS auth_users_tenant_email_uidx
    ON auth_users (tenant_id, lower(email));

-- +goose Down
DROP INDEX IF EXISTS auth_users_tenant_email_uidx;
ALTER TABLE auth_users ADD CONSTRAINT auth_users_email_unique UNIQUE (email);

DROP INDEX IF EXISTS provider_webhook_receipts_tenant_connector_event_uidx;
ALTER TABLE provider_webhook_receipts ADD CONSTRAINT provider_webhook_receipts_connector_id_event_id_key UNIQUE (connector_id, event_id);

DROP INDEX IF EXISTS connectors_tenant_provider_connector_uidx;
ALTER TABLE connectors ADD CONSTRAINT unique_provider_connector UNIQUE (provider, connector_id);
