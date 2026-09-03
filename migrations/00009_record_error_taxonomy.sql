-- +goose Up
-- docs/PHASE5_5_IMPLEMENTATION.md Unit Z: Razorpay's four-field error
-- taxonomy (error_code, error_description, error_source, error_step,
-- error_reason: https://razorpay.com/docs/webhooks/payloads/payments/),
-- additive alongside the existing failure_code column, never a replacement
-- for it. All five NULLable: every one of these is optional on the wire
-- (docs/API_GATEWAY.md), and NULL means "the caller never sent it", not "we
-- do not know" -- a distinct fact worth keeping distinct from an empty
-- string.
--
-- error_source and error_step are deliberately plain TEXT, not a CHECK
-- constraint against a fixed list: Razorpay's own docs say the possible
-- values for both vary by payment method (cards, netbanking, wallets, UPI
-- Intent, Cardless EMI and e-mandate each have their own sets), so this
-- schema does not freeze one method's vocabulary as if it were the whole
-- taxonomy.
ALTER TABLE record
    ADD COLUMN error_code        TEXT,
    ADD COLUMN error_description TEXT,
    ADD COLUMN error_source      TEXT,
    ADD COLUMN error_step        TEXT,
    ADD COLUMN error_reason      TEXT;

-- +goose Down
ALTER TABLE record
    DROP COLUMN IF EXISTS error_code,
    DROP COLUMN IF EXISTS error_description,
    DROP COLUMN IF EXISTS error_source,
    DROP COLUMN IF EXISTS error_step,
    DROP COLUMN IF EXISTS error_reason;
