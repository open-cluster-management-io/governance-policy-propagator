BEGIN;

DROP EXTENSION IF EXISTS pgcrypto;

-- Drop message_hash
ALTER TABLE compliance_events DROP CONSTRAINT compliance_events_cluster_id_policy_id_parent_policy_id_com_key;
DROP INDEX compliance_events_null1;
ALTER TABLE compliance_events DROP COLUMN message_hash;
ALTER TABLE compliance_events ADD CONSTRAINT compliance_events_cluster_id_policy_id_parent_policy_id_com_key UNIQUE (cluster_id, policy_id, parent_policy_id, compliance, message, timestamp);
CREATE UNIQUE INDEX compliance_events_null1 ON compliance_events (cluster_id, policy_id, compliance, message, timestamp) WHERE parent_policy_id IS NULL;
DROP INDEX compliance_events_messages;


-- Add back the spec column directly on the policies table
ALTER TABLE policies ADD spec JSONB;

DO
$DO$
DECLARE temprow RECORD;
BEGIN FOR temprow IN
    SELECT "id", "spec_id" FROM policies
  LOOP
    UPDATE policies SET spec = (SELECT spec FROM specs WHERE id=temprow.spec_id) WHERE id = temprow.id;
  END LOOP;
END;
$DO$;

ALTER TABLE policies DROP CONSTRAINT policies_kind_api_group_name_namespace_spec_id_severity_key;
DROP INDEX policies_null1;
DROP INDEX policies_null2;
DROP INDEX policies_null3;

ALTER TABLE policies DROP COLUMN spec_id;

DROP TABLE specs;

ALTER TABLE policies ADD CONSTRAINT policies_kind_api_group_name_namespace_spec_severity_key UNIQUE (kind, api_group, name, namespace, spec, severity);
CREATE UNIQUE INDEX policies_null1 ON policies (kind, api_group, name, spec, severity) WHERE namespace IS NULL;
CREATE UNIQUE INDEX policies_null2 ON policies (kind, api_group, name, namespace, spec) WHERE severity IS NULL;
CREATE UNIQUE INDEX policies_null3 ON policies (kind, api_group, name, spec) WHERE namespace IS NULL AND severity IS NULL;
CREATE INDEX idx_policies_spec ON policies (spec);

COMMIT;
