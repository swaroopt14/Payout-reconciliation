CREATE UNIQUE INDEX IF NOT EXISTS routing_decisions_merchant_payment_uidx
    ON routing_decisions (COALESCE(merchant_id, ''), payment_id);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'routing_decisions_payment_id_key'
    ) THEN
        ALTER TABLE routing_decisions DROP CONSTRAINT routing_decisions_payment_id_key;
    END IF;
END $$;
