CREATE DATABASE IF NOT EXISTS superbiz_agent
  CHARACTER SET utf8mb4
  COLLATE utf8mb4_unicode_ci;

USE superbiz_agent;

CREATE TABLE IF NOT EXISTS conversations (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  conversation_id VARCHAR(128) NOT NULL,
  user_id VARCHAR(128) NOT NULL,
  title VARCHAR(255) NOT NULL DEFAULT '',
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_conversation_id (conversation_id),
  KEY idx_user_updated (user_id, updated_at)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS conversation_turns (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  conversation_id VARCHAR(128) NOT NULL,
  role VARCHAR(32) NOT NULL,
  content LONGTEXT NOT NULL,
  metadata JSON NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_conversation_created (conversation_id, created_at),
  KEY idx_conversation_id_id (conversation_id, id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS conversation_summaries (
  conversation_id VARCHAR(128) NOT NULL,
  summary LONGTEXT NOT NULL,
  covers_through_turn_id BIGINT UNSIGNED NULL,
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (conversation_id),
  KEY idx_covers_through_turn_id (covers_through_turn_id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS session_states (
  conversation_id VARCHAR(128) NOT NULL,
  goal TEXT NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  state JSON NOT NULL,
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (conversation_id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS durable_memories (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id VARCHAR(128) NOT NULL,
  memory_key VARCHAR(128) NOT NULL,
  kind VARCHAR(64) NOT NULL,
  content TEXT NOT NULL,
  metadata JSON NULL,
  confidence DECIMAL(5,4) NOT NULL DEFAULT 1.0000,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  vector_status VARCHAR(32) NOT NULL DEFAULT 'pending',
  source_conversation_id VARCHAR(128) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_memory_key (user_id, memory_key),
  KEY idx_user_status_updated (user_id, status, updated_at),
  KEY idx_source_conversation_id (source_conversation_id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS durable_memory_candidates (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id VARCHAR(128) NOT NULL,
  memory_key VARCHAR(128) NOT NULL,
  kind VARCHAR(64) NOT NULL,
  content TEXT NOT NULL,
  confidence DECIMAL(5,4) NOT NULL DEFAULT 1.0000,
  source_conversation_id VARCHAR(128) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_candidate_key (user_id, memory_key),
  KEY idx_user_candidate_updated (user_id, updated_at)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS tool_results (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  conversation_id VARCHAR(128) NOT NULL,
  tool_name VARCHAR(128) NOT NULL,
  input JSON NULL,
  output_summary TEXT NOT NULL,
  output JSON NULL,
  status VARCHAR(32) NOT NULL,
  evidence_id VARCHAR(64) NULL,
  failed_query_id VARCHAR(64) NULL,
  can_use_as_evidence TINYINT(1) NOT NULL DEFAULT 0,
  data_status VARCHAR(32) NOT NULL DEFAULT 'unknown',
  subject JSON NULL,
  scope JSON NULL,
  observations JSON NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_conversation_created (conversation_id, created_at),
  KEY idx_tool_name_created (tool_name, created_at),
  KEY idx_conversation_evidence (conversation_id, evidence_id),
  KEY idx_conversation_failed_query (conversation_id, failed_query_id)
) ENGINE=InnoDB;
