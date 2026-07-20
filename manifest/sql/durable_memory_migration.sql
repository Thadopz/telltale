USE superbiz_agent;

ALTER TABLE durable_memories
  ADD COLUMN memory_key VARCHAR(128) NULL AFTER user_id,
  ADD COLUMN vector_status VARCHAR(32) NOT NULL DEFAULT 'pending' AFTER status;

UPDATE durable_memories
SET memory_key = CONCAT('legacy.', id)
WHERE memory_key IS NULL OR memory_key = '';

ALTER TABLE durable_memories
  MODIFY COLUMN memory_key VARCHAR(128) NOT NULL,
  ADD UNIQUE KEY uk_user_memory_key (user_id, memory_key);

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
