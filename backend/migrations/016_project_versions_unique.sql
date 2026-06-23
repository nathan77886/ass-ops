DELETE FROM project_versions older
USING project_versions newer
WHERE older.project_id = newer.project_id
  AND older.version = newer.version
  AND (
      older.created_at < newer.created_at
      OR (older.created_at = newer.created_at AND older.id::text < newer.id::text)
  );

CREATE UNIQUE INDEX IF NOT EXISTS idx_project_versions_project_version
    ON project_versions(project_id, version);
