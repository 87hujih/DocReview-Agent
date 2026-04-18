WITH chunk_defaults AS (
    SELECT chunks.id,
           COALESCE(
               NULLIF(BTRIM(chunks.section_type), ''),
               sections.section_type,
               CASE
                   WHEN NULLIF(BTRIM(chunks.section_title), '') IS NULL OR chunks.section_title = '全文' THEN 'document'
                   ELSE 'section'
               END
           ) AS normalized_section_type,
           COALESCE(NULLIF(BTRIM(chunks.chunk_role), ''), 'section_body') AS normalized_chunk_role,
           COALESCE(
               NULLIF(BTRIM(chunks.window_group_id), ''),
               NULLIF(BTRIM(sections.section_key), ''),
               NULLIF(BTRIM(chunks.section_title), ''),
               ''
           ) AS normalized_window_group_id,
           CASE
               WHEN chunks.page_start IS NOT NULL THEN chunks.page_start
               WHEN sections.page_start IS NOT NULL THEN sections.page_start
               WHEN chunks.page_end IS NOT NULL THEN chunks.page_end
               WHEN sections.page_end IS NOT NULL THEN sections.page_end
               ELSE 0
           END AS normalized_page_start,
           CASE
               WHEN chunks.page_end IS NOT NULL THEN chunks.page_end
               WHEN sections.page_end IS NOT NULL THEN sections.page_end
               WHEN chunks.page_start IS NOT NULL THEN chunks.page_start
               WHEN sections.page_start IS NOT NULL THEN sections.page_start
               ELSE 0
           END AS normalized_page_end,
           COALESCE(chunks.metadata_json, '{}'::jsonb) AS normalized_metadata_json
    FROM resource_chunks AS chunks
    LEFT JOIN resource_sections AS sections
      ON sections.id = chunks.section_id
)
UPDATE resource_chunks AS chunks
SET section_type = chunk_defaults.normalized_section_type,
    chunk_role = chunk_defaults.normalized_chunk_role,
    window_group_id = chunk_defaults.normalized_window_group_id,
    page_start = chunk_defaults.normalized_page_start,
    page_end = chunk_defaults.normalized_page_end,
    metadata_json = chunk_defaults.normalized_metadata_json
FROM chunk_defaults
WHERE chunks.id = chunk_defaults.id
  AND (
      chunks.section_type IS NULL
      OR BTRIM(chunks.section_type) = ''
      OR chunks.chunk_role IS NULL
      OR BTRIM(chunks.chunk_role) = ''
      OR chunks.window_group_id IS NULL
      OR chunks.page_start IS NULL
      OR chunks.page_end IS NULL
      OR chunks.metadata_json IS NULL
  );

ALTER TABLE resource_chunks
    ALTER COLUMN section_type SET DEFAULT 'document',
    ALTER COLUMN chunk_role SET DEFAULT 'section_body',
    ALTER COLUMN window_group_id SET DEFAULT '',
    ALTER COLUMN page_start SET DEFAULT 0,
    ALTER COLUMN page_end SET DEFAULT 0,
    ALTER COLUMN metadata_json SET DEFAULT '{}'::jsonb;

ALTER TABLE resource_chunks
    ALTER COLUMN section_type SET NOT NULL,
    ALTER COLUMN chunk_role SET NOT NULL,
    ALTER COLUMN window_group_id SET NOT NULL,
    ALTER COLUMN page_start SET NOT NULL,
    ALTER COLUMN page_end SET NOT NULL,
    ALTER COLUMN metadata_json SET NOT NULL;
