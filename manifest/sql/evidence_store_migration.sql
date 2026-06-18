USE superbiz_agent;

ALTER TABLE tool_results
  ADD COLUMN evidence_id VARCHAR(64) NULL,
  ADD COLUMN failed_query_id VARCHAR(64) NULL,
  ADD COLUMN can_use_as_evidence TINYINT(1) NOT NULL DEFAULT 0,
  ADD COLUMN data_status VARCHAR(32) NOT NULL DEFAULT 'unknown',
  ADD COLUMN subject JSON NULL,
  ADD COLUMN scope JSON NULL,
  ADD COLUMN observations JSON NULL,
  ADD KEY idx_conversation_evidence (conversation_id, evidence_id),
  ADD KEY idx_conversation_failed_query (conversation_id, failed_query_id);
