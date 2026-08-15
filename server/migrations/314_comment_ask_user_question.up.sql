ALTER TABLE comment
  ADD COLUMN metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  ADD CONSTRAINT comment_metadata_object CHECK (jsonb_typeof(metadata) = 'object'),
  ADD CONSTRAINT comment_metadata_size CHECK (pg_column_size(metadata) <= 8192);
