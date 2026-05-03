UPDATE resource_version_structures
SET source_format = COALESCE(source_format, ''),
    parser_name = COALESCE(parser_name, ''),
    parser_version = COALESCE(parser_version, ''),
    quality_flags_json = COALESCE(quality_flags_json, '[]'::jsonb)
WHERE source_format IS NULL
   OR parser_name IS NULL
   OR parser_version IS NULL
   OR quality_flags_json IS NULL;

ALTER TABLE resource_version_structures
    ALTER COLUMN source_format SET DEFAULT '',
    ALTER COLUMN parser_name SET DEFAULT '',
    ALTER COLUMN parser_version SET DEFAULT '',
    ALTER COLUMN quality_flags_json SET DEFAULT '[]'::jsonb;

ALTER TABLE resource_version_structures
    ALTER COLUMN source_format SET NOT NULL,
    ALTER COLUMN parser_name SET NOT NULL,
    ALTER COLUMN parser_version SET NOT NULL,
    ALTER COLUMN quality_flags_json SET NOT NULL;
