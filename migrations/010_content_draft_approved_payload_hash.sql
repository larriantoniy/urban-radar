-- Existing APPROVED drafts deliberately remain without this value: their
-- historical approval did not bind a media payload and Publisher V0 must fail
-- closed rather than infer that approval retroactively.
ALTER TABLE content_drafts
    ADD COLUMN approved_payload_hash TEXT;
