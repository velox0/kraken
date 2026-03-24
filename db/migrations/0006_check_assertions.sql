-- Migration: Replace expected_status with flexible assertions JSONB column

-- Step 1: Add the new assertions column
ALTER TABLE checks ADD COLUMN IF NOT EXISTS assertions JSONB NOT NULL DEFAULT '[]';

-- Step 2: Migrate existing expected_status values into assertions
UPDATE checks
SET assertions = jsonb_build_array(
    jsonb_build_object(
        'type', 'status',
        'operator', 'eq',
        'value', expected_status::TEXT
    )
)
WHERE expected_status IS NOT NULL;

-- Step 3: Drop the old column
ALTER TABLE checks DROP COLUMN IF EXISTS expected_status;

-- Rollback (manual):
-- ALTER TABLE checks ADD COLUMN expected_status INTEGER;
-- UPDATE checks SET expected_status = (assertions->0->>'value')::INTEGER
--   WHERE jsonb_array_length(assertions) = 1
--     AND assertions->0->>'type' = 'status'
--     AND assertions->0->>'operator' = 'eq';
-- ALTER TABLE checks DROP COLUMN assertions;
