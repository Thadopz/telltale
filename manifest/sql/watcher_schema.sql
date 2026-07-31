USE superbiz_agent;

CREATE TABLE IF NOT EXISTS detector_states (
  state_key VARCHAR(255) NOT NULL,
  rule_id VARCHAR(128) NOT NULL,
  state VARCHAR(32) NOT NULL DEFAULT 'normal',
  consecutive_failures INT NOT NULL DEFAULT 0,
  consecutive_successes INT NOT NULL DEFAULT 0,
  last_value DOUBLE NOT NULL DEFAULT 0,
  last_evidence_id VARCHAR(64) NULL,
  last_error TEXT NULL,
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (state_key),
  KEY idx_detector_rule_updated (rule_id, updated_at)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS incidents (
  id VARCHAR(64) NOT NULL,
  fingerprint VARCHAR(255) NOT NULL,
  rule_id VARCHAR(128) NOT NULL,
  domain VARCHAR(64) NOT NULL,
  status VARCHAR(32) NOT NULL,
  severity VARCHAR(32) NOT NULL,
  first_seen_at DATETIME(3) NOT NULL,
  last_seen_at DATETIME(3) NOT NULL,
  resolved_at DATETIME(3) NULL,
  latest_value DOUBLE NOT NULL DEFAULT 0,
  evidence_id VARCHAR(64) NULL,
  evidence_json JSON NULL,
  diagnosis LONGTEXT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_incident_fingerprint (fingerprint),
  KEY idx_incident_status_last_seen (status, last_seen_at),
  KEY idx_incident_rule_last_seen (rule_id, last_seen_at)
) ENGINE=InnoDB;
