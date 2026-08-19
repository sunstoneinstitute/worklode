-- Watermark for the backbone→knowledge-graph projector (spec 006 §11): one
-- row; last_state_log_id is the state_log id through which task changes have
-- been projected. The index serves DirtyProjects' (entity_kind, id > $1)
-- scan, so non-task rows are never touched.
CREATE TABLE graph_projection (
    id                integer PRIMARY KEY CHECK (id = 1),
    last_state_log_id bigint  NOT NULL DEFAULT 0
);
INSERT INTO graph_projection (id, last_state_log_id) VALUES (1, 0);

CREATE INDEX state_log_kind_id ON state_log (entity_kind, id);
