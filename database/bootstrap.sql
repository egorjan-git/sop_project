\set ON_ERROR_STOP on
BEGIN;
\ir ../sop_project/migrations/001_resource_catalog.sql
\ir ../sop_project/migrations/002_sheet_snapshots.sql
\ir ../zulip-collector/migrations/001_zulip.sql
\ir ../zulip-collector/migrations/002_sync_targets.sql
\ir ../zulip-collector/migrations/003_page_progress.sql
\ir ../database/migrations/001_integration.sql
COMMIT;
