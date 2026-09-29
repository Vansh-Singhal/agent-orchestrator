-- +goose Up
-- +goose StatementBegin
-- Classify why a turn failed, so a consumer can tell a refused credential from
-- a lost response (issue #5967).
--
-- A provider 504 or dropped stream means the request was accepted and the
-- outcome was never reported, so work the agent had already started may have
-- landed. Without this column every failure read the same as a clean rejection,
-- and no caller could decide whether re-running the turn was safe.
--
-- The empty string is the default: a turn that did not fail has no cause to
-- classify, and saying "unknown" about it would imply AO tried and failed to
-- classify a cause that does not exist. A turn that *did* fail is always written
-- with a real class, and a cause AO could not classify is stored as 'ambiguous'
-- so it is never mistaken for proof that nothing ran.
ALTER TABLE conversation_turns ADD COLUMN error_class TEXT NOT NULL DEFAULT ''
    CHECK (error_class IN ('', 'unknown', 'ambiguous', 'transient', 'permanent'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE conversation_turns DROP COLUMN error_class;
-- +goose StatementEnd
