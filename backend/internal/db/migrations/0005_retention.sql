-- Default retention 14 days; admins may raise it to at most 180 days per project.
UPDATE projects SET retention_days = 14 WHERE retention_days = 30;
UPDATE projects SET retention_days = 180 WHERE retention_days > 180;
ALTER TABLE projects ALTER COLUMN retention_days SET DEFAULT 14;
ALTER TABLE projects ADD CONSTRAINT projects_retention_max_chk CHECK (retention_days <= 180);
