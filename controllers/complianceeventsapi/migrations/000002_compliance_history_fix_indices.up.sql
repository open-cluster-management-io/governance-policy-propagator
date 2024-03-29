BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- If compliance messages are too long, the unique index gets too large and fails. This is a workaround for a unique
-- constraint while still allowing for long messages.

-- Drop the soon to be invalid unique constraints.
ALTER TABLE compliance_events DROP CONSTRAINT compliance_events_cluster_id_policy_id_parent_policy_id_com_key;
DROP INDEX compliance_events_null1;

-- SHA1 hex of the message for the uniqueness constraint.
ALTER TABLE compliance_events ADD message_hash VARCHAR(40);

-- Populate the SHA1 hex
DO
$DO$
DECLARE temprow RECORD;
BEGIN FOR temprow IN
    SELECT "id", "message" FROM compliance_events
  LOOP
    UPDATE compliance_events SET message_hash = encode(digest(temprow.message, 'sha1'), 'hex') WHERE id = temprow.id;
  END LOOP;
END;
$DO$;

ALTER TABLE compliance_events ALTER COLUMN message_hash SET NOT NULL;

-- Set the unique constraints
ALTER TABLE compliance_events ADD CONSTRAINT compliance_events_cluster_id_policy_id_parent_policy_id_com_key UNIQUE (cluster_id, policy_id, parent_policy_id, compliance, message_hash, timestamp);

CREATE UNIQUE INDEX compliance_events_null1 ON compliance_events (cluster_id, policy_id, compliance, message_hash, timestamp) WHERE parent_policy_id IS NULL;

-- Provide an index for equality comparisons on the message.
CREATE INDEX compliance_events_messages ON compliance_events USING HASH (message);

-- If the spec is too long, the unique index gets too large and fails. This is a workaround for a unique
-- constraint while still allowing for spec uniqueness.

CREATE TABLE specs(
  id serial PRIMARY KEY,
  spec JSONB NOT NULL,
  EXCLUDE USING HASH (spec with =)
);

-- Drop the soon to be invalid unique constraints.
ALTER TABLE policies DROP CONSTRAINT policies_kind_api_group_name_namespace_spec_severity_key;
DROP INDEX policies_null1;
DROP INDEX policies_null2;
DROP INDEX policies_null3;
DROP INDEX idx_policies_spec;

ALTER TABLE policies ADD spec_id INT;
ALTER TABLE policies ADD FOREIGN KEY (spec_id) REFERENCES specs(id);

-- Populate the specs table
DO
$DO$
DECLARE temprow RECORD;
BEGIN FOR temprow IN
    SELECT "id", "spec" FROM policies
  LOOP
    INSERT INTO specs (spec) VALUES (temprow.spec) ON CONFLICT DO NOTHING;
    UPDATE policies SET spec_id = (SELECT id FROM specs WHERE spec=temprow.spec) WHERE id = temprow.id;
  END LOOP;
END;
$DO$;

ALTER TABLE policies ALTER COLUMN spec_id SET NOT NULL;

ALTER TABLE policies DROP spec;

ALTER TABLE policies ADD CONSTRAINT policies_kind_api_group_name_namespace_spec_id_severity_key UNIQUE (kind, api_group, name, namespace, spec_id, severity);
CREATE UNIQUE INDEX policies_null1 ON policies (kind, api_group, name, spec_id, severity) WHERE namespace IS NULL;
CREATE UNIQUE INDEX policies_null2 ON policies (kind, api_group, name, namespace, spec_id) WHERE severity IS NULL;
CREATE UNIQUE INDEX policies_null3 ON policies (kind, api_group, name, spec_id) WHERE namespace IS NULL AND severity IS NULL;

COMMIT;
