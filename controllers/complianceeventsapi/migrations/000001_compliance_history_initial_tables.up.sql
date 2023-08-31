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
   categories TEXT [],
   controls TEXT [],
   standards TEXT [],
   UNIQUE (name, categories, controls, standards)
);

CREATE TABLE IF NOT EXISTS policies(
   id serial PRIMARY KEY,
   kind TEXT NOT NULL,
   api_group TEXT NOT NULL,
   name TEXT NOT NULL,
   namespace TEXT,
   parent_policy_id INT,
   spec TEXT,
   -- SHA1 hash
   spec_hash CHAR[40],
   severity TEXT,
   CONSTRAINT fk_parent_policy_id
      FOREIGN KEY(parent_policy_id) 
	  REFERENCES parent_policies(id),
   UNIQUE (kind, api_group, name, namespace, parent_policy_id, spec_hash, severity)
);

CREATE INDEX IF NOT EXISTS idx_policies_spec_hash ON policies (spec_hash);

CREATE TABLE IF NOT EXISTS compliance_events(
   id serial PRIMARY KEY,
   cluster_id INT NOT NULL,
   policy_id INT NOT NULL,
   compliance TEXT NOT NULL,
   message TEXT NOT NULL,
   timestamp TIMESTAMP NOT NULL,
   metadata TEXT,
   reported_by TEXT,
   CONSTRAINT fk_policy_id
      FOREIGN KEY(policy_id) 
	  REFERENCES policies(id),
   CONSTRAINT fk_cluster_id
      FOREIGN KEY(cluster_id) 
	  REFERENCES clusters(id)
);

CREATE INDEX IF NOT EXISTS idx_compliance_events_compliance ON compliance_events (compliance);
CREATE INDEX IF NOT EXISTS idx_compliance_events_timestamp ON compliance_events (timestamp);
CREATE INDEX IF NOT EXISTS idx_compliance_events_reported_by ON compliance_events (reported_by);

COMMIT;
