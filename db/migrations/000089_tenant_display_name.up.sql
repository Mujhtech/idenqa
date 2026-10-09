ALTER TABLE idenqa.tenants
    ADD COLUMN display_name text;

-- Existing installations predate tenant presentation metadata. Preserve their
-- readability with an explicit identifier fallback; operators must replace it
-- with the organisation display name before issuing new hosted launches.
UPDATE idenqa.tenants
SET display_name = id
WHERE display_name IS NULL;

ALTER TABLE idenqa.tenants
    ALTER COLUMN display_name SET NOT NULL,
    ADD CONSTRAINT tenants_display_name CHECK (
        char_length(display_name) BETWEEN 1 AND 200 AND
        display_name = btrim(display_name) AND
        display_name !~ '[[:cntrl:]]'
    );
