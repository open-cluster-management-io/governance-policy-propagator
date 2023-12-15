BEGIN;

CREATE TABLE IF NOT EXISTS clusters(
   id serial PRIMARY KEY,
   name TEXT NOT NULL,
   cluster_id TEXT UNIQUE NOT NULL,
   UNIQUE (name, cluster_id)
);

CREATE INDEX IF NOT EXISTS idx_clusters_name ON clusters (name);

CREATE TABLE IF NOT EXISTS parent_policies(
   id serial PRIMARY KEY,
   name TEXT NOT NULL,
   namespace TEXT NOT NULL,
   categories TEXT [],
   controls TEXT [],
   standards TEXT [],
   UNIQUE (name, namespace, categories, controls, standards)
);

-- This is required until we only support Postgres 15+ to utilize NULLS NOT DISTINCT.
-- Partial indexes with 1 nullable unique field provided (e.g. A, B, C)
CREATE UNIQUE INDEX parent_policies_null1 ON parent_policies (name, namespace, controls, standards) WHERE categories IS NULL;
CREATE UNIQUE INDEX parent_policies_null2 ON parent_policies (name, namespace, categories, standards) WHERE controls IS NULL;
CREATE UNIQUE INDEX parent_policies_null3 ON parent_policies (name, namespace, categories, controls) WHERE standards IS NULL;

-- Partial indexes with 2 nullable unique field provided (e.g. AB AC BC)
CREATE UNIQUE INDEX parent_policies_null4 ON parent_policies (name, namespace, standards) WHERE categories IS NULL AND controls IS NULL;
CREATE UNIQUE INDEX parent_policies_null5 ON parent_policies (name, namespace, controls) WHERE categories IS NULL AND standards IS NULL;
CREATE UNIQUE INDEX parent_policies_null6 ON parent_policies (name, namespace, categories) WHERE controls IS NULL AND standards IS NULL;

-- Partial index with no nullable unique fields provided (e.g. ABC)
CREATE UNIQUE INDEX parent_policies_null7 ON parent_policies (name, namespace) WHERE categories IS NULL AND controls IS NULL AND standards IS NULL;

CREATE TABLE IF NOT EXISTS policies(
   id serial PRIMARY KEY,
   kind TEXT NOT NULL,
   api_group TEXT NOT NULL,
   name TEXT NOT NULL,
   namespace TEXT,
   spec TEXT NOT NULL,
   -- SHA1 hash
   spec_hash CHAR(40) NOT NULL,
   severity TEXT,
   UNIQUE (kind, api_group, name, namespace, spec_hash, severity)
);

-- This is required until we only support Postgres 15+ to utilize NULLS NOT DISTINCT.
-- Partial indexes with 1 nullable unique field provided (e.g. A, B)
CREATE UNIQUE INDEX policies_null1 ON policies (kind, api_group, name, spec_hash, severity) WHERE namespace IS NULL;
CREATE UNIQUE INDEX policies_null2 ON policies (kind, api_group, name, namespace, spec_hash) WHERE severity IS NULL;

-- Partial index with no nullable unique fields provided (e.g. AB)
CREATE UNIQUE INDEX policies_null3 ON policies (kind, api_group, name, spec_hash) WHERE namespace IS NULL AND severity IS NULL;

CREATE INDEX IF NOT EXISTS idx_policies_spec_hash ON policies (spec_hash);

CREATE TABLE IF NOT EXISTS compliance_events(
   id serial PRIMARY KEY,
   cluster_id INT NOT NULL,
   policy_id INT NOT NULL,
   parent_policy_id INT,
   compliance TEXT NOT NULL,
   message TEXT NOT NULL,
   timestamp TIMESTAMP NOT NULL,
   metadata JSONB,
   reported_by TEXT,
   CONSTRAINT fk_policy_id
      FOREIGN KEY(policy_id) 
	  REFERENCES policies(id),
   CONSTRAINT fk_parent_policy_id
      FOREIGN KEY(parent_policy_id)
	  REFERENCES parent_policies(id),
   CONSTRAINT fk_cluster_id
      FOREIGN KEY(cluster_id) 
	  REFERENCES clusters(id)
);

CREATE INDEX IF NOT EXISTS idx_compliance_events_compliance ON compliance_events (compliance);
CREATE INDEX IF NOT EXISTS idx_compliance_events_timestamp ON compliance_events (timestamp);
CREATE INDEX IF NOT EXISTS idx_compliance_events_reported_by ON compliance_events (reported_by);

COMMIT;
