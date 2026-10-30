-- Baseline: the schema and seed rows migrations 0001-0100 produced, squashed
-- from a pg_dump of a fresh database at version 100. A database already past
-- 100 never runs this file.

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE public.actor_event_cursor (
    actor_id text NOT NULL,
    last_event_id bigint NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT actor_event_cursor_last_event_id_check CHECK ((last_event_id >= 0))
);

CREATE TABLE public.actors (
    id text NOT NULL,
    kind text NOT NULL,
    display_name text,
    admin boolean DEFAULT false NOT NULL,
    github_username text,
    groups jsonb,
    email text,
    CONSTRAINT actors_kind_check CHECK ((kind = ANY (ARRAY['human'::text, 'agent'::text, 'service'::text])))
);

CREATE TABLE public.agent_session_usage (
    agent_session_id bigint NOT NULL,
    usage_day date NOT NULL,
    model text NOT NULL,
    speed text DEFAULT 'standard'::text NOT NULL,
    input_tokens bigint DEFAULT 0 NOT NULL,
    cache_write_5m_tokens bigint DEFAULT 0 NOT NULL,
    cache_write_1h_tokens bigint DEFAULT 0 NOT NULL,
    cache_read_tokens bigint DEFAULT 0 NOT NULL,
    output_tokens bigint DEFAULT 0 NOT NULL,
    cost_amount numeric(14,6),
    cost_currency text DEFAULT 'USD'::text NOT NULL,
    CONSTRAINT agent_session_usage_currency_format CHECK ((cost_currency ~ '^[A-Z]{3}$'::text)),
    CONSTRAINT agent_session_usage_nonnegative CHECK (((input_tokens >= 0) AND (cache_write_5m_tokens >= 0) AND (cache_write_1h_tokens >= 0) AND (cache_read_tokens >= 0) AND (output_tokens >= 0))),
    CONSTRAINT agent_session_usage_speed_known CHECK ((speed = ANY (ARRAY['standard'::text, 'fast'::text])))
);

CREATE TABLE public.agent_sessions (
    id bigint NOT NULL,
    lease_id bigint NOT NULL,
    agent text NOT NULL,
    agent_version text,
    external_session_id text NOT NULL,
    started_at timestamp with time zone NOT NULL,
    last_seen_at timestamp with time zone NOT NULL,
    ended_at timestamp with time zone,
    input_tokens bigint,
    output_tokens bigint,
    cost_amount numeric(12,6),
    cost_currency text DEFAULT 'USD'::text NOT NULL,
    CONSTRAINT agent_sessions_agent_known CHECK ((agent = ANY (ARRAY['claude-code'::text, 'codex'::text, 'copilot'::text, 'cursor'::text, 'aider'::text, 'opencode'::text, 'pi'::text, 'amp'::text, 'other'::text]))),
    CONSTRAINT agent_sessions_cost_currency_format CHECK ((cost_currency ~ '^[A-Z]{3}$'::text))
);

ALTER TABLE public.agent_sessions ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.agent_sessions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.approvals (
    id bigint NOT NULL,
    entity_kind text NOT NULL,
    entity_id text NOT NULL,
    subject_revision text NOT NULL,
    required_role text,
    required_actor text,
    resolving_actor text,
    state text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    resolved_at timestamp with time zone,
    lane text DEFAULT ''::text NOT NULL,
    created_by text,
    review_kind text DEFAULT 'review'::text NOT NULL,
    note text,
    exception_authorized_by text,
    CONSTRAINT approvals_review_kind_check CHECK ((review_kind = ANY (ARRAY['review'::text, 'impact'::text]))),
    CONSTRAINT approvals_state_check CHECK ((state = ANY (ARRAY['awaiting'::text, 'approved'::text, 'rejected'::text, 'changes_requested'::text])))
);

ALTER TABLE public.approvals ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.approvals_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.artifact_declarations (
    id bigint NOT NULL,
    entity_kind text NOT NULL,
    entity_id text NOT NULL,
    artifact_uri text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    selector text DEFAULT 'address'::text NOT NULL,
    CONSTRAINT artifact_declarations_entity_kind_check CHECK ((entity_kind = ANY (ARRAY['deliverable'::text, 'task'::text, 'doc'::text]))),
    CONSTRAINT artifact_declarations_selector_check CHECK ((selector = ANY (ARRAY['address'::text, 'label'::text])))
);

ALTER TABLE public.artifact_declarations ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.artifact_declarations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.artifact_evidence (
    id bigint NOT NULL,
    entity_kind text NOT NULL,
    entity_id text NOT NULL,
    artifact_uri text NOT NULL,
    source text NOT NULL,
    state text NOT NULL,
    provenance text NOT NULL,
    version text DEFAULT ''::text NOT NULL,
    url text DEFAULT ''::text NOT NULL,
    detail jsonb,
    event_id bigint NOT NULL,
    occurred_at timestamp with time zone NOT NULL,
    CONSTRAINT artifact_evidence_entity_kind_check CHECK ((entity_kind = ANY (ARRAY['deliverable'::text, 'task'::text, 'doc'::text]))),
    CONSTRAINT artifact_evidence_provenance_check CHECK ((provenance = ANY (ARRAY['observed'::text, 'user_reported'::text]))),
    CONSTRAINT artifact_evidence_state_check CHECK ((state = ANY (ARRAY['published'::text, 'updated'::text, 'deprecated'::text, 'removed'::text, 'failed'::text])))
);

ALTER TABLE public.artifact_evidence ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.artifact_evidence_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.artifacts (
    id bigint NOT NULL,
    kind text NOT NULL,
    name text NOT NULL,
    version text NOT NULL,
    digest text,
    repo text,
    source_sha text,
    built_at timestamp with time zone,
    CONSTRAINT artifacts_kind_check CHECK ((kind = ANY (ARRAY['docker_image'::text, 'pypi'::text, 'git_tag'::text, 'binary'::text])))
);

ALTER TABLE public.artifacts ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.artifacts_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.blobs (
    hash text NOT NULL,
    media_type text NOT NULL,
    size bigint NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT blobs_hash_format CHECK ((hash ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT blobs_size_positive CHECK ((size >= 0))
);

CREATE TABLE public.ci_runs (
    id bigint NOT NULL,
    repo text NOT NULL,
    head_sha text NOT NULL,
    workflow text NOT NULL,
    status text,
    conclusion text,
    url text,
    started_at timestamp with time zone NOT NULL,
    completed_at timestamp with time zone,
    updated_at timestamp with time zone
);

ALTER TABLE public.ci_runs ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.ci_runs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.doc_edges (
    id bigint NOT NULL,
    from_doc bigint NOT NULL,
    from_anchor text,
    type text NOT NULL,
    to_doc bigint,
    to_anchor text,
    to_external text,
    to_rule bigint,
    owner_doc bigint,
    owner_external text,
    CONSTRAINT doc_edges_blocked_by_whole_docs CHECK (((type <> 'blockedBy'::text) OR ((from_anchor IS NULL) AND (to_anchor IS NULL)))),
    CONSTRAINT doc_edges_covers_rule CHECK (
CASE
    WHEN (type = 'covers'::text) THEN ((to_doc IS NULL) AND (to_anchor IS NULL))
    ELSE (to_rule IS NULL)
END),
    CONSTRAINT doc_edges_defers_from_doc CHECK (((type <> 'defers'::text) OR (from_anchor IS NULL))),
    CONSTRAINT doc_edges_one_target CHECK ((num_nonnulls(to_doc, to_rule, to_external) = 1)),
    CONSTRAINT doc_edges_owner_on_defers CHECK ((num_nonnulls(owner_doc, owner_external) <=
CASE
    WHEN (type = 'defers'::text) THEN 1
    ELSE 0
END)),
    CONSTRAINT doc_edges_type_check CHECK ((type = ANY (ARRAY['covers'::text, 'implements'::text, 'requires'::text, 'wasDerivedFrom'::text, 'blockedBy'::text, 'defers'::text])))
);

CREATE TABLE public.rule_edges (
    from_rule bigint NOT NULL,
    to_rule bigint NOT NULL,
    type text NOT NULL,
    source text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT rule_edges_check CHECK ((from_rule <> to_rule)),
    CONSTRAINT rule_edges_source_check CHECK ((source = ANY (ARRAY['manual'::text, 'derived'::text, 'refactor'::text]))),
    CONSTRAINT rule_edges_type_check CHECK ((type = ANY (ARRAY['refines'::text, 'constrains'::text, 'conflictsWith'::text, 'references'::text, 'amends'::text, 'supersedes'::text, 'wasDerivedFrom'::text])))
);

CREATE VIEW public.covered_rules AS
 WITH RECURSIVE walk(edge_id, plan_id, rule_id) AS (
         SELECT doc_edges.id,
            doc_edges.from_doc,
            doc_edges.to_rule
           FROM public.doc_edges
          WHERE ((doc_edges.type = 'covers'::text) AND (doc_edges.to_rule IS NOT NULL))
        UNION
         SELECT w.edge_id,
            w.plan_id,
            s.from_rule
           FROM (walk w
             JOIN public.rule_edges s ON (((s.to_rule = w.rule_id) AND (s.type = 'supersedes'::text))))
        )
 SELECT edge_id,
    plan_id,
    rule_id
   FROM walk;

CREATE TABLE public.doc_rules (
    doc_id bigint NOT NULL,
    "position" integer NOT NULL,
    rule_id bigint NOT NULL,
    rule_version integer NOT NULL,
    depth integer NOT NULL,
    anchor text NOT NULL
);

CREATE VIEW public.covered_sections AS
 SELECT c.edge_id,
    c.plan_id,
    c.rule_id,
    dr.doc_id,
    dr.anchor
   FROM (public.covered_rules c
     JOIN public.doc_rules dr ON ((dr.rule_id = c.rule_id)));

CREATE TABLE public.decisions (
    id bigint NOT NULL,
    task_id text NOT NULL,
    key text NOT NULL,
    "position" integer NOT NULL,
    "group" text DEFAULT ''::text NOT NULL,
    question text NOT NULL,
    context text DEFAULT ''::text NOT NULL,
    response_type text NOT NULL,
    options jsonb,
    min_picks integer,
    max_picks integer,
    answer jsonb,
    decided_by text,
    decided_at timestamp with time zone,
    CONSTRAINT decisions_response_type_check CHECK ((response_type = ANY (ARRAY['single_select'::text, 'multi_select'::text, 'single_select_notes'::text, 'pick_or_freetext'::text, 'yes_no'::text, 'freetext'::text])))
);

ALTER TABLE public.decisions ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.decisions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.deliverables (
    id text NOT NULL,
    project_id text NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    url text DEFAULT ''::text NOT NULL,
    created_by text,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    milestone_id text
);

CREATE TABLE public.deploy_shas (
    repo text NOT NULL,
    sha text NOT NULL,
    main_id bigint NOT NULL
);

CREATE TABLE public.deployments (
    id bigint NOT NULL,
    artifact_id bigint,
    environment text NOT NULL,
    target_kind text NOT NULL,
    target_name text NOT NULL,
    status text NOT NULL,
    first_seen timestamp with time zone NOT NULL,
    last_update timestamp with time zone NOT NULL,
    CONSTRAINT deployments_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'reconciling'::text, 'deployed'::text, 'failed'::text]))),
    CONSTRAINT deployments_target_kind_check CHECK ((target_kind = ANY (ARRAY['flux_kustomization'::text, 'pypi'::text, 'manual'::text])))
);

ALTER TABLE public.deployments ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.deployments_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.doc_edge_versions (
    doc_id bigint NOT NULL,
    version integer NOT NULL,
    from_anchor text,
    type text NOT NULL,
    to_doc bigint,
    to_anchor text,
    to_external text,
    to_rule bigint,
    owner_doc bigint,
    owner_external text
);

ALTER TABLE public.doc_edges ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.doc_edges_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.doc_notes (
    id bigint NOT NULL,
    doc_id bigint NOT NULL,
    anchor text NOT NULL,
    body text NOT NULL,
    task_id text,
    session_id text,
    created_by text,
    created_at timestamp with time zone NOT NULL
);

ALTER TABLE public.doc_notes ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.doc_notes_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.doc_reviewers (
    doc_id bigint NOT NULL,
    actor_id text NOT NULL,
    assigned_at timestamp with time zone NOT NULL
);

CREATE TABLE public.doc_revision_edges (
    id bigint NOT NULL,
    doc_id bigint NOT NULL,
    from_anchor text,
    type text NOT NULL,
    to_doc bigint,
    to_anchor text,
    to_external text,
    to_rule bigint,
    owner_doc bigint,
    owner_external text,
    CONSTRAINT doc_revision_edges_blocked_by_whole_docs CHECK (((type <> 'blockedBy'::text) OR ((from_anchor IS NULL) AND (to_anchor IS NULL)))),
    CONSTRAINT doc_revision_edges_covers_rule CHECK (
CASE
    WHEN (type = 'covers'::text) THEN ((to_doc IS NULL) AND (to_anchor IS NULL))
    ELSE (to_rule IS NULL)
END),
    CONSTRAINT doc_revision_edges_defers_from_doc CHECK (((type <> 'defers'::text) OR (from_anchor IS NULL))),
    CONSTRAINT doc_revision_edges_one_target CHECK ((num_nonnulls(to_doc, to_rule, to_external) = 1)),
    CONSTRAINT doc_revision_edges_owner_on_defers CHECK ((num_nonnulls(owner_doc, owner_external) <=
CASE
    WHEN (type = 'defers'::text) THEN 1
    ELSE 0
END)),
    CONSTRAINT doc_revision_edges_type_check CHECK ((type = ANY (ARRAY['covers'::text, 'implements'::text, 'requires'::text, 'wasDerivedFrom'::text, 'blockedBy'::text, 'defers'::text])))
);

ALTER TABLE public.doc_revision_edges ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.doc_revision_edges_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.doc_revisions (
    doc_id bigint NOT NULL,
    body text NOT NULL,
    created_by text,
    created_at timestamp with time zone NOT NULL
);

CREATE TABLE public.doc_rule_versions (
    doc_id bigint NOT NULL,
    version integer NOT NULL,
    "position" integer NOT NULL,
    rule_id bigint NOT NULL,
    rule_version integer NOT NULL,
    depth integer NOT NULL,
    anchor text NOT NULL
);

CREATE TABLE public.doc_sections (
    doc_id bigint NOT NULL,
    anchor text NOT NULL,
    number text,
    heading text NOT NULL,
    depth integer NOT NULL,
    "position" integer NOT NULL,
    last_revised_in integer DEFAULT 1 NOT NULL,
    published boolean DEFAULT false NOT NULL,
    patched boolean DEFAULT false NOT NULL
);

CREATE TABLE public.doc_versions (
    doc_id bigint NOT NULL,
    version integer NOT NULL,
    body text NOT NULL,
    title text NOT NULL,
    issued date,
    created_at timestamp with time zone NOT NULL
);

CREATE TABLE public.docs (
    id bigint NOT NULL,
    project_id text NOT NULL,
    kind text NOT NULL,
    number integer NOT NULL,
    slug text NOT NULL,
    title text NOT NULL,
    body text NOT NULL,
    status text DEFAULT 'draft'::text NOT NULL,
    version integer DEFAULT 1 NOT NULL,
    issued date,
    owner text,
    created_by text,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone,
    deleted_by text,
    delete_justification text,
    generated_by_task text,
    CONSTRAINT docs_kind_check CHECK ((kind = ANY (ARRAY['spec'::text, 'adr'::text, 'plan'::text]))),
    CONSTRAINT docs_status_check CHECK ((status = ANY (ARRAY['draft'::text, 'accepted'::text, 'stale'::text, 'superseded'::text, 'withdrawn'::text, 'spent'::text])))
);

ALTER TABLE public.docs ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.docs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.embedding_config (
    singleton boolean DEFAULT true NOT NULL,
    provider_id text NOT NULL,
    CONSTRAINT embedding_config_singleton_check CHECK (singleton)
);

CREATE TABLE public.entity_edges (
    from_kind text NOT NULL,
    from_id text NOT NULL,
    to_kind text NOT NULL,
    to_id text NOT NULL,
    rel text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    created_by text,
    CONSTRAINT entity_edges_from_kind_check CHECK ((from_kind = ANY (ARRAY['project'::text, 'milestone'::text, 'doc'::text, 'deliverable'::text, 'task'::text, 'pr'::text]))),
    CONSTRAINT entity_edges_rel_check CHECK ((rel = ANY (ARRAY['depends_on'::text, 'seeded_by'::text, 'references_revision'::text]))),
    CONSTRAINT entity_edges_to_kind_check CHECK ((to_kind = ANY (ARRAY['deliverable'::text, 'task'::text, 'doc'::text, 'pr'::text])))
);

CREATE TABLE public.env_deploys (
    repo text NOT NULL,
    environment text NOT NULL,
    gh_main_id bigint,
    flux_main_id bigint,
    flux_seen boolean DEFAULT false NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT env_deploys_environment_check CHECK ((environment = ANY (ARRAY['dev'::text, 'prod'::text])))
);

CREATE TABLE public.event_subscribers (
    name text NOT NULL,
    last_read_offset bigint DEFAULT 0 NOT NULL,
    last_acked_offset bigint DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT event_subscribers_acked_le_read CHECK ((last_acked_offset <= last_read_offset))
);

CREATE TABLE public.events (
    id bigint NOT NULL,
    source text NOT NULL,
    external_id text NOT NULL,
    type text NOT NULL,
    payload jsonb,
    received_at timestamp with time zone NOT NULL,
    txid xid8 DEFAULT pg_current_xact_id() NOT NULL,
    applied_at timestamp with time zone,
    CONSTRAINT events_source_check CHECK ((source = ANY (ARRAY['github'::text, 'flux'::text, 'watcher'::text, 'cli'::text, 'system'::text, 'web'::text, 'catalog'::text, 'cms'::text, 'ci'::text, 'pipeline'::text, 'prober'::text])))
);

ALTER TABLE public.events ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.github_user_tokens (
    actor_id text NOT NULL,
    ciphertext bytea NOT NULL,
    updated_at timestamp with time zone NOT NULL
);

CREATE TABLE public.graph_projection (
    id integer NOT NULL,
    last_txid bigint DEFAULT 0 NOT NULL,
    CONSTRAINT graph_projection_id_check CHECK ((id = 1))
);

CREATE TABLE public.graph_projection_failures (
    project_id text NOT NULL,
    attempts integer NOT NULL,
    first_failed_at timestamp with time zone NOT NULL,
    last_failed_at timestamp with time zone NOT NULL,
    next_attempt_at timestamp with time zone NOT NULL,
    last_error text NOT NULL
);

CREATE TABLE public.index_chunks (
    id bigint NOT NULL,
    subject_kind text NOT NULL,
    doc_id bigint,
    task_id text,
    skill_id bigint,
    project text,
    anchor text DEFAULT ''::text NOT NULL,
    chunk_index integer NOT NULL,
    context_header text DEFAULT ''::text NOT NULL,
    chunk_text text NOT NULL,
    content_hash text NOT NULL,
    embedding public.vector(768),
    tsv tsvector GENERATED ALWAYS AS ((setweight(to_tsvector('simple'::regconfig, context_header), 'A'::"char") || setweight(to_tsvector('simple'::regconfig, chunk_text), 'B'::"char"))) STORED,
    indexed_at timestamp with time zone NOT NULL,
    CONSTRAINT index_chunks_kind_matches_subject CHECK ((((subject_kind = 'doc'::text) AND (doc_id IS NOT NULL)) OR ((subject_kind = 'task'::text) AND (task_id IS NOT NULL)) OR ((subject_kind = 'skill'::text) AND (skill_id IS NOT NULL)))),
    CONSTRAINT index_chunks_one_subject CHECK ((num_nonnulls(doc_id, task_id, skill_id) = 1)),
    CONSTRAINT index_chunks_subject_kind_check CHECK ((subject_kind = ANY (ARRAY['doc'::text, 'task'::text, 'skill'::text])))
);

ALTER TABLE public.index_chunks ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.index_chunks_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.issues (
    repo text NOT NULL,
    number integer NOT NULL,
    title text,
    state text,
    triage_state text DEFAULT 'new'::text NOT NULL,
    task_id text,
    applies_to_versions text,
    url text,
    updated_at timestamp with time zone,
    CONSTRAINT issues_triage_state_check CHECK ((triage_state = ANY (ARRAY['new'::text, 'promoted'::text, 'dismissed'::text])))
);

CREATE TABLE public.leases (
    id bigint NOT NULL,
    task_id text NOT NULL,
    actor_id text NOT NULL,
    worktree text NOT NULL,
    acquired_at timestamp with time zone NOT NULL,
    renewed_at timestamp with time zone,
    expires_at timestamp with time zone NOT NULL,
    released_at timestamp with time zone
);

ALTER TABLE public.leases ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.leases_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.main_commits (
    id bigint NOT NULL,
    repo text NOT NULL,
    sha text NOT NULL,
    pushed_at timestamp with time zone NOT NULL
);

ALTER TABLE public.main_commits ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.main_commits_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.milestones (
    id text NOT NULL,
    project_id text NOT NULL,
    title text NOT NULL,
    "position" integer NOT NULL,
    created_by text,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);

CREATE TABLE public.model_prices (
    model text NOT NULL,
    speed text DEFAULT 'standard'::text NOT NULL,
    effective_from date NOT NULL,
    currency text DEFAULT 'USD'::text NOT NULL,
    input_micros bigint NOT NULL,
    cache_write_5m_micros bigint NOT NULL,
    cache_write_1h_micros bigint NOT NULL,
    cache_read_micros bigint NOT NULL,
    output_micros bigint NOT NULL,
    CONSTRAINT model_prices_currency_format CHECK ((currency ~ '^[A-Z]{3}$'::text)),
    CONSTRAINT model_prices_nonnegative CHECK (((input_micros >= 0) AND (cache_write_5m_micros >= 0) AND (cache_write_1h_micros >= 0) AND (cache_read_micros >= 0) AND (output_micros >= 0))),
    CONSTRAINT model_prices_speed_known CHECK ((speed = ANY (ARRAY['standard'::text, 'fast'::text])))
);

CREATE TABLE public.project_daily_cost (
    project_id text NOT NULL,
    usage_day date NOT NULL,
    cost_currency text DEFAULT 'USD'::text NOT NULL,
    input_tokens bigint DEFAULT 0 NOT NULL,
    cache_write_5m_tokens bigint DEFAULT 0 NOT NULL,
    cache_write_1h_tokens bigint DEFAULT 0 NOT NULL,
    cache_read_tokens bigint DEFAULT 0 NOT NULL,
    output_tokens bigint DEFAULT 0 NOT NULL,
    cost_amount numeric(14,6) DEFAULT 0 NOT NULL,
    unpriced_tokens bigint DEFAULT 0 NOT NULL,
    CONSTRAINT project_daily_cost_currency_format CHECK ((cost_currency ~ '^[A-Z]{3}$'::text))
);

CREATE TABLE public.project_daily_overhead_cost (
    project_id text NOT NULL,
    usage_day date NOT NULL,
    cost_currency text DEFAULT 'USD'::text NOT NULL,
    input_tokens bigint DEFAULT 0 NOT NULL,
    cache_write_5m_tokens bigint DEFAULT 0 NOT NULL,
    cache_write_1h_tokens bigint DEFAULT 0 NOT NULL,
    cache_read_tokens bigint DEFAULT 0 NOT NULL,
    output_tokens bigint DEFAULT 0 NOT NULL,
    cost_amount numeric(14,6) DEFAULT 0 NOT NULL,
    unpriced_tokens bigint DEFAULT 0 NOT NULL,
    CONSTRAINT project_daily_overhead_cost_currency_format CHECK ((cost_currency ~ '^[A-Z]{3}$'::text))
);

CREATE TABLE public.project_entity_seq (
    project_id text NOT NULL,
    kind text NOT NULL,
    next bigint NOT NULL,
    CONSTRAINT project_entity_seq_kind_check CHECK ((kind = ANY (ARRAY['DEL'::text, 'PLAN'::text, 'SPEC'::text, 'ADR'::text, 'MILE'::text, 'RULE'::text])))
);

CREATE TABLE public.project_overhead_usage (
    project_id text NOT NULL,
    agent text NOT NULL,
    external_session_id text NOT NULL,
    usage_day date NOT NULL,
    model text NOT NULL,
    speed text DEFAULT 'standard'::text NOT NULL,
    input_tokens bigint DEFAULT 0 NOT NULL,
    cache_write_5m_tokens bigint DEFAULT 0 NOT NULL,
    cache_write_1h_tokens bigint DEFAULT 0 NOT NULL,
    cache_read_tokens bigint DEFAULT 0 NOT NULL,
    output_tokens bigint DEFAULT 0 NOT NULL,
    cost_amount numeric(14,6),
    cost_currency text DEFAULT 'USD'::text NOT NULL,
    CONSTRAINT project_overhead_usage_currency_format CHECK ((cost_currency ~ '^[A-Z]{3}$'::text)),
    CONSTRAINT project_overhead_usage_nonnegative CHECK (((input_tokens >= 0) AND (cache_write_5m_tokens >= 0) AND (cache_write_1h_tokens >= 0) AND (cache_read_tokens >= 0) AND (output_tokens >= 0))),
    CONSTRAINT project_overhead_usage_speed_known CHECK ((speed = ANY (ARRAY['standard'::text, 'fast'::text])))
);

CREATE TABLE public.project_participants (
    project_id text NOT NULL,
    actor_id text NOT NULL,
    role text NOT NULL,
    is_lead boolean DEFAULT false NOT NULL,
    added_at timestamp with time zone NOT NULL,
    added_by text,
    is_deputy boolean DEFAULT false NOT NULL,
    CONSTRAINT project_participants_lead_deputy_exclusive CHECK ((NOT (is_lead AND is_deputy))),
    CONSTRAINT project_participants_role_check CHECK ((role = ANY (ARRAY['member'::text, 'editor'::text, 'science-lead'::text, 'reporter'::text, 'domain-expert'::text, 'data-scientist'::text, 'engineer'::text])))
);

CREATE TABLE public.project_repos (
    project_id text NOT NULL,
    repo text NOT NULL,
    done_state text DEFAULT 'merged'::text NOT NULL,
    mapped_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT project_repos_done_state_check CHECK ((done_state = ANY (ARRAY['merged'::text, 'deployed_prod'::text, 'released'::text])))
);

CREATE TABLE public.projects (
    id text NOT NULL,
    name text NOT NULL,
    focus jsonb DEFAULT '[]'::jsonb NOT NULL,
    key text NOT NULL,
    next_task_num bigint DEFAULT 1 NOT NULL,
    focus_note text,
    focus_pinned_by text,
    focus_pinned_at timestamp with time zone,
    decision_title text,
    decision_accountable text,
    decision_readiness text,
    approval_flow jsonb,
    approval_flow_name text,
    approval_flow_rev text,
    labels jsonb DEFAULT '{}'::jsonb NOT NULL,
    horizon text DEFAULT 'standing'::text NOT NULL,
    doc_staleness_days integer,
    settings jsonb DEFAULT '{}'::jsonb NOT NULL,
    CONSTRAINT projects_doc_staleness_days_check CHECK ((doc_staleness_days > 0)),
    CONSTRAINT projects_horizon_check CHECK ((horizon = ANY (ARRAY['bounded'::text, 'standing'::text]))),
    CONSTRAINT projects_key_format CHECK (((key ~ '^[A-Z][A-Z0-9]{1,9}$'::text) AND (key <> ALL (ARRAY['SPEC'::text, 'ADR'::text]))))
);

CREATE TABLE public.pull_requests (
    repo text NOT NULL,
    number integer NOT NULL,
    title text,
    state text NOT NULL,
    task_id text,
    head_ref text,
    head_sha text,
    merge_sha text,
    url text,
    opened_at timestamp with time zone,
    merged_at timestamp with time zone,
    updated_at timestamp with time zone,
    author text,
    queued_at timestamp with time zone,
    CONSTRAINT pull_requests_state_check CHECK ((state = ANY (ARRAY['open'::text, 'merged'::text, 'closed'::text])))
);

CREATE TABLE public.release_frontiers (
    repo text NOT NULL,
    tag text NOT NULL,
    main_id bigint NOT NULL,
    published_at timestamp with time zone NOT NULL
);

CREATE TABLE public.repo_branch_rules (
    repo text NOT NULL,
    branch text NOT NULL,
    merge_queue boolean NOT NULL,
    checked_at timestamp with time zone NOT NULL,
    pull_request boolean
);

CREATE TABLE public.reviews (
    id bigint NOT NULL,
    repo text NOT NULL,
    pr_number integer NOT NULL,
    reviewer text NOT NULL,
    state text NOT NULL,
    submitted_at timestamp with time zone NOT NULL,
    CONSTRAINT reviews_state_check CHECK ((state = ANY (ARRAY['approved'::text, 'changes_requested'::text, 'commented'::text])))
);

ALTER TABLE public.reviews ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.reviews_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.rule_edge_versions (
    rule_id bigint NOT NULL,
    version integer NOT NULL,
    to_rule bigint NOT NULL,
    type text NOT NULL,
    source text NOT NULL
);

CREATE TABLE public.rule_versions (
    rule_id bigint NOT NULL,
    version integer NOT NULL,
    heading text NOT NULL,
    body text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE public.rules (
    id bigint NOT NULL,
    project_id text NOT NULL,
    number bigint NOT NULL,
    status text DEFAULT 'draft'::text NOT NULL,
    version integer DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    owner text,
    tags text[] DEFAULT '{}'::text[] NOT NULL,
    kind text DEFAULT 'requirement'::text NOT NULL,
    CONSTRAINT rules_kind_check CHECK ((kind = ANY (ARRAY['requirement'::text, 'invariant'::text, 'informative'::text]))),
    CONSTRAINT rules_status_check CHECK ((status = ANY (ARRAY['draft'::text, 'accepted'::text, 'superseded'::text, 'withdrawn'::text])))
);

CREATE SEQUENCE public.rules_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.rules_id_seq OWNED BY public.rules.id;

CREATE TABLE public.runtime_events (
    id bigint NOT NULL,
    cluster text NOT NULL,
    kind text NOT NULL,
    workload text,
    image text,
    artifact_id bigint,
    message text,
    occurred_at timestamp with time zone NOT NULL,
    CONSTRAINT runtime_events_kind_check CHECK ((kind = ANY (ARRAY['crashloop'::text, 'oom'::text, 'flux_failure'::text, 'flux_recovery'::text])))
);

ALTER TABLE public.runtime_events ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.runtime_events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.skill_versions (
    id bigint NOT NULL,
    skill_id bigint NOT NULL,
    git_commit text NOT NULL,
    content_hash text NOT NULL,
    frontmatter jsonb NOT NULL,
    skill_md text NOT NULL,
    archive bytea NOT NULL,
    created_at timestamp with time zone NOT NULL
);

ALTER TABLE public.skill_versions ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.skill_versions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.skills (
    id bigint NOT NULL,
    name text NOT NULL,
    description text NOT NULL,
    source_repo text NOT NULL,
    source_path text NOT NULL,
    latest_version_id bigint,
    deleted_at timestamp with time zone,
    qualifier text DEFAULT ''::text NOT NULL,
    CONSTRAINT skills_qualifier_nonempty CHECK ((qualifier <> ''::text))
);

ALTER TABLE public.skills ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.skills_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.state_log (
    id bigint NOT NULL,
    entity_kind text NOT NULL,
    entity_id text NOT NULL,
    change jsonb NOT NULL,
    event_id bigint NOT NULL,
    at timestamp with time zone NOT NULL,
    txid xid8 DEFAULT pg_current_xact_id() NOT NULL
);

ALTER TABLE public.state_log ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.state_log_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.task_activity (
    id bigint NOT NULL,
    task_id text NOT NULL,
    actor_id text NOT NULL,
    agent text NOT NULL,
    session_id text NOT NULL,
    at timestamp with time zone NOT NULL,
    event text NOT NULL,
    attrs jsonb DEFAULT '{}'::jsonb NOT NULL,
    recorded_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE public.task_activity ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.task_activity_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.task_blobs (
    task_id text NOT NULL,
    hash text NOT NULL,
    filename text NOT NULL,
    embedded boolean DEFAULT false NOT NULL,
    attached boolean DEFAULT false NOT NULL,
    created_by text,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT task_blobs_referenced CHECK ((embedded OR attached))
);

CREATE TABLE public.task_commits (
    task_id text NOT NULL,
    repo text NOT NULL,
    sha text NOT NULL,
    source text NOT NULL,
    seen_at timestamp with time zone NOT NULL,
    CONSTRAINT task_commits_source_check CHECK ((source = ANY (ARRAY['branch_push'::text, 'pr'::text, 'merge_message'::text, 'marker'::text, 'local_merge'::text])))
);

CREATE TABLE public.task_edges (
    from_task text NOT NULL,
    to_task text NOT NULL,
    type text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT task_edges_type_check CHECK ((type = ANY (ARRAY['child_of'::text, 'blocks'::text, 'follow_up_to'::text, 'duplicate_of'::text])))
);

CREATE TABLE public.task_governed_by (
    task_id text NOT NULL,
    rule_id bigint NOT NULL,
    rule_version integer NOT NULL,
    source text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    pinned_version integer,
    CONSTRAINT task_governed_by_source_check CHECK ((source = ANY (ARRAY['plan'::text, 'manual'::text, 'gate'::text])))
);

CREATE TABLE public.task_instructions (
    id bigint NOT NULL,
    task_id text NOT NULL,
    body text NOT NULL,
    created_by text,
    created_at timestamp with time zone NOT NULL,
    delivered_at timestamp with time zone
);

CREATE SEQUENCE public.task_instructions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.task_instructions_id_seq OWNED BY public.task_instructions.id;

CREATE TABLE public.tasks (
    id text NOT NULL,
    project_id text NOT NULL,
    title text NOT NULL,
    body text,
    priority text NOT NULL,
    kind text NOT NULL,
    state text NOT NULL,
    created_by text,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    concern text,
    needs_decomposition boolean DEFAULT false NOT NULL,
    skills jsonb DEFAULT '[]'::jsonb NOT NULL,
    assignee text,
    secrets jsonb DEFAULT '[]'::jsonb NOT NULL,
    plan_doc bigint,
    about_doc bigint,
    deleted_at timestamp with time zone,
    deleted_by text,
    delete_justification text,
    plan_task_key text,
    human_only boolean DEFAULT false NOT NULL,
    milestone_id text,
    about_anchor text,
    CONSTRAINT tasks_concern_check CHECK ((concern = ANY (ARRAY['completeness'::text, 'performance'::text, 'usability'::text, 'security'::text]))),
    CONSTRAINT tasks_kind_check CHECK ((kind = ANY (ARRAY['feature'::text, 'bug'::text, 'chore'::text, 'design'::text, 'review'::text, 'spike'::text, 'decision'::text, 'rally'::text]))),
    CONSTRAINT tasks_plan_task_key_with_plan_doc CHECK (((plan_doc IS NULL) = (plan_task_key IS NULL))),
    CONSTRAINT tasks_priority_check CHECK ((priority = ANY (ARRAY['critical'::text, 'high'::text, 'medium'::text, 'low'::text]))),
    CONSTRAINT tasks_state_check CHECK ((state = ANY (ARRAY['draft'::text, 'ready'::text, 'in_progress'::text, 'in_review'::text, 'merged'::text, 'deployed_dev'::text, 'deployed_prod'::text, 'released'::text, 'abandoned'::text])))
);

CREATE TABLE public.tokens (
    token_hash text NOT NULL,
    actor_id text NOT NULL,
    description text,
    created_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone,
    revoked_at timestamp with time zone,
    task_id text
);

ALTER TABLE ONLY public.rules ALTER COLUMN id SET DEFAULT nextval('public.rules_id_seq'::regclass);

ALTER TABLE ONLY public.task_instructions ALTER COLUMN id SET DEFAULT nextval('public.task_instructions_id_seq'::regclass);

ALTER TABLE ONLY public.actor_event_cursor
    ADD CONSTRAINT actor_event_cursor_pkey PRIMARY KEY (actor_id);

ALTER TABLE ONLY public.actors
    ADD CONSTRAINT actors_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.agent_session_usage
    ADD CONSTRAINT agent_session_usage_pkey PRIMARY KEY (agent_session_id, usage_day, model, speed);

ALTER TABLE ONLY public.agent_sessions
    ADD CONSTRAINT agent_sessions_lease_session_unique UNIQUE (lease_id, agent, external_session_id);

ALTER TABLE ONLY public.agent_sessions
    ADD CONSTRAINT agent_sessions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.approvals
    ADD CONSTRAINT approvals_entity_revision_lane_review_kind_key UNIQUE (entity_kind, entity_id, subject_revision, lane, review_kind);

ALTER TABLE ONLY public.approvals
    ADD CONSTRAINT approvals_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.artifact_declarations
    ADD CONSTRAINT artifact_declarations_entity_kind_entity_id_artifact_uri_key UNIQUE (entity_kind, entity_id, artifact_uri);

ALTER TABLE ONLY public.artifact_declarations
    ADD CONSTRAINT artifact_declarations_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.artifact_evidence
    ADD CONSTRAINT artifact_evidence_entity_kind_entity_id_artifact_uri_event__key UNIQUE (entity_kind, entity_id, artifact_uri, event_id);

ALTER TABLE ONLY public.artifact_evidence
    ADD CONSTRAINT artifact_evidence_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.artifacts
    ADD CONSTRAINT artifacts_kind_name_version_key UNIQUE (kind, name, version);

ALTER TABLE ONLY public.artifacts
    ADD CONSTRAINT artifacts_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.blobs
    ADD CONSTRAINT blobs_pkey PRIMARY KEY (hash);

ALTER TABLE ONLY public.ci_runs
    ADD CONSTRAINT ci_runs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.ci_runs
    ADD CONSTRAINT ci_runs_repo_head_sha_workflow_started_at_key UNIQUE (repo, head_sha, workflow, started_at);

ALTER TABLE ONLY public.decisions
    ADD CONSTRAINT decisions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.decisions
    ADD CONSTRAINT decisions_task_id_key_key UNIQUE (task_id, key);

ALTER TABLE ONLY public.deliverables
    ADD CONSTRAINT deliverables_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.deploy_shas
    ADD CONSTRAINT deploy_shas_pkey PRIMARY KEY (repo, sha);

ALTER TABLE ONLY public.deployments
    ADD CONSTRAINT deployments_environment_target_kind_target_name_key UNIQUE (environment, target_kind, target_name);

ALTER TABLE ONLY public.deployments
    ADD CONSTRAINT deployments_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.doc_edges
    ADD CONSTRAINT doc_edges_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.doc_notes
    ADD CONSTRAINT doc_notes_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.doc_reviewers
    ADD CONSTRAINT doc_reviewers_pkey PRIMARY KEY (doc_id, actor_id);

ALTER TABLE ONLY public.doc_revision_edges
    ADD CONSTRAINT doc_revision_edges_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.doc_revisions
    ADD CONSTRAINT doc_revisions_pkey PRIMARY KEY (doc_id);

ALTER TABLE ONLY public.doc_rule_versions
    ADD CONSTRAINT doc_rule_versions_pkey PRIMARY KEY (doc_id, version, "position");

ALTER TABLE ONLY public.doc_rules
    ADD CONSTRAINT doc_rules_pkey PRIMARY KEY (doc_id, "position");

ALTER TABLE ONLY public.doc_sections
    ADD CONSTRAINT doc_sections_pkey PRIMARY KEY (doc_id, anchor);

ALTER TABLE ONLY public.doc_versions
    ADD CONSTRAINT doc_versions_pkey PRIMARY KEY (doc_id, version);

ALTER TABLE ONLY public.docs
    ADD CONSTRAINT docs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.embedding_config
    ADD CONSTRAINT embedding_config_pkey PRIMARY KEY (singleton);

ALTER TABLE ONLY public.entity_edges
    ADD CONSTRAINT entity_edges_pkey PRIMARY KEY (from_kind, from_id, to_kind, to_id, rel);

ALTER TABLE ONLY public.env_deploys
    ADD CONSTRAINT env_deploys_pkey PRIMARY KEY (repo, environment);

ALTER TABLE ONLY public.event_subscribers
    ADD CONSTRAINT event_subscribers_pkey PRIMARY KEY (name);

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_source_external_id_key UNIQUE (source, external_id);

ALTER TABLE ONLY public.github_user_tokens
    ADD CONSTRAINT github_user_tokens_pkey PRIMARY KEY (actor_id);

ALTER TABLE ONLY public.graph_projection_failures
    ADD CONSTRAINT graph_projection_failures_pkey PRIMARY KEY (project_id);

ALTER TABLE ONLY public.graph_projection
    ADD CONSTRAINT graph_projection_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.index_chunks
    ADD CONSTRAINT index_chunks_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.issues
    ADD CONSTRAINT issues_pkey PRIMARY KEY (repo, number);

ALTER TABLE ONLY public.leases
    ADD CONSTRAINT leases_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.main_commits
    ADD CONSTRAINT main_commits_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.main_commits
    ADD CONSTRAINT main_commits_repo_sha_key UNIQUE (repo, sha);

ALTER TABLE ONLY public.milestones
    ADD CONSTRAINT milestones_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.model_prices
    ADD CONSTRAINT model_prices_pkey PRIMARY KEY (model, speed, effective_from);

ALTER TABLE ONLY public.project_daily_cost
    ADD CONSTRAINT project_daily_cost_pkey PRIMARY KEY (project_id, usage_day, cost_currency);

ALTER TABLE ONLY public.project_daily_overhead_cost
    ADD CONSTRAINT project_daily_overhead_cost_pkey PRIMARY KEY (project_id, usage_day, cost_currency);

ALTER TABLE ONLY public.project_entity_seq
    ADD CONSTRAINT project_entity_seq_pkey PRIMARY KEY (project_id, kind);

ALTER TABLE ONLY public.project_overhead_usage
    ADD CONSTRAINT project_overhead_usage_pkey PRIMARY KEY (project_id, agent, external_session_id, usage_day, model, speed);

ALTER TABLE ONLY public.project_participants
    ADD CONSTRAINT project_participants_pkey PRIMARY KEY (project_id, actor_id, role);

ALTER TABLE ONLY public.project_repos
    ADD CONSTRAINT project_repos_pkey PRIMARY KEY (project_id, repo);

ALTER TABLE ONLY public.project_repos
    ADD CONSTRAINT project_repos_repo_key UNIQUE (repo);

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_key_unique UNIQUE (key);

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.pull_requests
    ADD CONSTRAINT pull_requests_pkey PRIMARY KEY (repo, number);

ALTER TABLE ONLY public.release_frontiers
    ADD CONSTRAINT release_frontiers_pkey PRIMARY KEY (repo, tag);

ALTER TABLE ONLY public.repo_branch_rules
    ADD CONSTRAINT repo_branch_rules_pkey PRIMARY KEY (repo, branch);

ALTER TABLE ONLY public.reviews
    ADD CONSTRAINT reviews_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.reviews
    ADD CONSTRAINT reviews_repo_pr_number_reviewer_submitted_at_key UNIQUE (repo, pr_number, reviewer, submitted_at);

ALTER TABLE ONLY public.rule_edge_versions
    ADD CONSTRAINT rule_edge_versions_pkey PRIMARY KEY (rule_id, version, to_rule, type);

ALTER TABLE ONLY public.rule_edges
    ADD CONSTRAINT rule_edges_pkey PRIMARY KEY (from_rule, to_rule, type);

ALTER TABLE ONLY public.rule_versions
    ADD CONSTRAINT rule_versions_pkey PRIMARY KEY (rule_id, version);

ALTER TABLE ONLY public.rules
    ADD CONSTRAINT rules_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.rules
    ADD CONSTRAINT rules_project_id_number_key UNIQUE (project_id, number);

ALTER TABLE ONLY public.runtime_events
    ADD CONSTRAINT runtime_events_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.skill_versions
    ADD CONSTRAINT skill_versions_hash_unique UNIQUE (skill_id, content_hash);

ALTER TABLE ONLY public.skill_versions
    ADD CONSTRAINT skill_versions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.skills
    ADD CONSTRAINT skills_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.skills
    ADD CONSTRAINT skills_qualified_name_unique UNIQUE (qualifier, name);

ALTER TABLE ONLY public.state_log
    ADD CONSTRAINT state_log_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.task_activity
    ADD CONSTRAINT task_activity_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.task_blobs
    ADD CONSTRAINT task_blobs_pkey PRIMARY KEY (task_id, hash);

ALTER TABLE ONLY public.task_commits
    ADD CONSTRAINT task_commits_pkey PRIMARY KEY (task_id, repo, sha);

ALTER TABLE ONLY public.task_edges
    ADD CONSTRAINT task_edges_from_task_to_task_type_key UNIQUE (from_task, to_task, type);

ALTER TABLE ONLY public.task_governed_by
    ADD CONSTRAINT task_governed_by_pkey PRIMARY KEY (task_id, rule_id);

ALTER TABLE ONLY public.task_instructions
    ADD CONSTRAINT task_instructions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.tokens
    ADD CONSTRAINT tokens_pkey PRIMARY KEY (token_hash);

CREATE UNIQUE INDEX actors_github_username_unique ON public.actors USING btree (lower(github_username)) WHERE (github_username IS NOT NULL);

CREATE INDEX agent_session_usage_day ON public.agent_session_usage USING btree (usage_day);

CREATE INDEX approvals_awaiting_idx ON public.approvals USING btree (entity_kind, entity_id) WHERE (state = ANY (ARRAY['awaiting'::text, 'changes_requested'::text]));

CREATE INDEX artifact_declarations_uri_idx ON public.artifact_declarations USING btree (artifact_uri);

CREATE INDEX artifact_evidence_entity_idx ON public.artifact_evidence USING btree (entity_kind, entity_id, occurred_at DESC, id DESC);

CREATE INDEX artifacts_digest_idx ON public.artifacts USING btree (digest) WHERE (digest IS NOT NULL);

CREATE INDEX decisions_task_position_idx ON public.decisions USING btree (task_id, "position");

CREATE INDEX deliverables_milestone_idx ON public.deliverables USING btree (milestone_id) WHERE (milestone_id IS NOT NULL);

CREATE INDEX deliverables_project_idx ON public.deliverables USING btree (project_id, created_at, id);

CREATE INDEX doc_edge_versions_doc ON public.doc_edge_versions USING btree (doc_id, version);

CREATE INDEX doc_edges_to ON public.doc_edges USING btree (to_doc) WHERE (to_doc IS NOT NULL);

CREATE INDEX doc_edges_to_rule ON public.doc_edges USING btree (to_rule) WHERE (to_rule IS NOT NULL);

CREATE UNIQUE INDEX doc_edges_unique ON public.doc_edges USING btree (from_doc, COALESCE(from_anchor, ''::text), type, COALESCE(to_doc, (0)::bigint), COALESCE(to_rule, (0)::bigint), COALESCE(to_anchor, ''::text), COALESCE(to_external, ''::text));

CREATE INDEX doc_notes_doc_anchor ON public.doc_notes USING btree (doc_id, anchor);

CREATE UNIQUE INDEX doc_revision_edges_unique ON public.doc_revision_edges USING btree (doc_id, COALESCE(from_anchor, ''::text), type, COALESCE(to_doc, (0)::bigint), COALESCE(to_rule, (0)::bigint), COALESCE(to_anchor, ''::text), COALESCE(to_external, ''::text));

CREATE INDEX doc_rules_rule ON public.doc_rules USING btree (rule_id);

CREATE INDEX docs_generated_by_task ON public.docs USING btree (generated_by_task) WHERE (generated_by_task IS NOT NULL);

CREATE INDEX docs_owner ON public.docs USING btree (owner) WHERE (owner IS NOT NULL);

CREATE UNIQUE INDEX docs_project_kind_number ON public.docs USING btree (project_id, kind, number) WHERE (deleted_at IS NULL);

CREATE UNIQUE INDEX docs_project_slug ON public.docs USING btree (project_id, slug) WHERE (deleted_at IS NULL);

CREATE INDEX entity_edges_to_idx ON public.entity_edges USING btree (to_kind, to_id);

CREATE INDEX events_txid_id ON public.events USING btree (txid, id);

CREATE INDEX events_unapplied ON public.events USING btree (id) WHERE ((applied_at IS NULL) AND (source = 'github'::text));

CREATE UNIQUE INDEX index_chunks_doc ON public.index_chunks USING btree (doc_id, anchor, chunk_index) WHERE (doc_id IS NOT NULL);

CREATE INDEX index_chunks_embedding ON public.index_chunks USING hnsw (embedding public.vector_cosine_ops);

CREATE INDEX index_chunks_kind_project ON public.index_chunks USING btree (subject_kind, project);

CREATE UNIQUE INDEX index_chunks_skill ON public.index_chunks USING btree (skill_id, chunk_index) WHERE (skill_id IS NOT NULL);

CREATE UNIQUE INDEX index_chunks_task ON public.index_chunks USING btree (task_id, chunk_index) WHERE (task_id IS NOT NULL);

CREATE INDEX index_chunks_tsv ON public.index_chunks USING gin (tsv);

CREATE UNIQUE INDEX leases_active ON public.leases USING btree (task_id) WHERE (released_at IS NULL);

CREATE UNIQUE INDEX leases_active_worktree ON public.leases USING btree (actor_id, worktree) WHERE (released_at IS NULL);

CREATE INDEX milestones_project_idx ON public.milestones USING btree (project_id, "position", id);

CREATE INDEX project_overhead_usage_day ON public.project_overhead_usage USING btree (usage_day);

CREATE UNIQUE INDEX project_participants_one_deputy ON public.project_participants USING btree (project_id) WHERE is_deputy;

CREATE UNIQUE INDEX project_participants_one_flag_per_actor ON public.project_participants USING btree (project_id, actor_id) WHERE (is_lead OR is_deputy);

CREATE UNIQUE INDEX project_participants_one_lead ON public.project_participants USING btree (project_id) WHERE is_lead;

CREATE UNIQUE INDEX rule_edges_symmetric_once ON public.rule_edges USING btree (LEAST(from_rule, to_rule), GREATEST(from_rule, to_rule), type) WHERE (type = 'conflictsWith'::text);

CREATE INDEX rule_edges_to ON public.rule_edges USING btree (to_rule);

CREATE INDEX state_log_entity ON public.state_log USING btree (entity_kind, entity_id, at DESC, id DESC);

CREATE INDEX state_log_kind_id ON public.state_log USING btree (entity_kind, id);

CREATE INDEX state_log_txid_id ON public.state_log USING btree (txid, id);

CREATE INDEX task_activity_at_idx ON public.task_activity USING btree (at);

CREATE INDEX task_activity_task_id_idx ON public.task_activity USING btree (task_id, id);

CREATE INDEX task_blobs_hash_idx ON public.task_blobs USING btree (hash);

CREATE INDEX task_commits_repo_sha ON public.task_commits USING btree (repo, sha);

CREATE INDEX task_edges_children ON public.task_edges USING btree (to_task) WHERE (type = 'child_of'::text);

CREATE UNIQUE INDEX task_edges_single_canonical ON public.task_edges USING btree (from_task) WHERE (type = 'duplicate_of'::text);

CREATE UNIQUE INDEX task_edges_single_origin ON public.task_edges USING btree (from_task) WHERE (type = 'follow_up_to'::text);

CREATE UNIQUE INDEX task_edges_single_parent ON public.task_edges USING btree (from_task) WHERE (type = 'child_of'::text);

CREATE INDEX task_governed_by_rule ON public.task_governed_by USING btree (rule_id);

CREATE INDEX task_instructions_pending ON public.task_instructions USING btree (task_id, id) WHERE (delivered_at IS NULL);

CREATE INDEX tasks_about_doc ON public.tasks USING btree (about_doc) WHERE (about_doc IS NOT NULL);

CREATE INDEX tasks_assignee ON public.tasks USING btree (assignee) WHERE (assignee IS NOT NULL);

CREATE INDEX tasks_milestone_idx ON public.tasks USING btree (milestone_id) WHERE (milestone_id IS NOT NULL);

CREATE UNIQUE INDEX tasks_one_active_rally ON public.tasks USING btree (project_id) WHERE ((kind = 'rally'::text) AND (state <> ALL (ARRAY['draft'::text, 'merged'::text, 'deployed_dev'::text, 'deployed_prod'::text, 'released'::text, 'abandoned'::text])) AND (deleted_at IS NULL));

CREATE UNIQUE INDEX tasks_one_draft_rally ON public.tasks USING btree (project_id) WHERE ((kind = 'rally'::text) AND (state = 'draft'::text) AND (deleted_at IS NULL));

CREATE INDEX tasks_plan_doc ON public.tasks USING btree (plan_doc) WHERE (plan_doc IS NOT NULL);

CREATE UNIQUE INDEX tasks_plan_task_key ON public.tasks USING btree (plan_doc, plan_task_key) WHERE (plan_doc IS NOT NULL);

CREATE INDEX tasks_updated_at_idx ON public.tasks USING btree (updated_at);

CREATE INDEX tokens_task_live ON public.tokens USING btree (task_id) WHERE ((task_id IS NOT NULL) AND (revoked_at IS NULL));

ALTER TABLE ONLY public.actor_event_cursor
    ADD CONSTRAINT actor_event_cursor_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES public.actors(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.agent_session_usage
    ADD CONSTRAINT agent_session_usage_agent_session_id_fkey FOREIGN KEY (agent_session_id) REFERENCES public.agent_sessions(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.agent_sessions
    ADD CONSTRAINT agent_sessions_lease_id_fkey FOREIGN KEY (lease_id) REFERENCES public.leases(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.approvals
    ADD CONSTRAINT approvals_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.actors(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.approvals
    ADD CONSTRAINT approvals_exception_authorized_by_fkey FOREIGN KEY (exception_authorized_by) REFERENCES public.actors(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.approvals
    ADD CONSTRAINT approvals_required_actor_fkey FOREIGN KEY (required_actor) REFERENCES public.actors(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.approvals
    ADD CONSTRAINT approvals_resolving_actor_fkey FOREIGN KEY (resolving_actor) REFERENCES public.actors(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.artifact_evidence
    ADD CONSTRAINT artifact_evidence_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.decisions
    ADD CONSTRAINT decisions_decided_by_fkey FOREIGN KEY (decided_by) REFERENCES public.actors(id);

ALTER TABLE ONLY public.decisions
    ADD CONSTRAINT decisions_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id);

ALTER TABLE ONLY public.deliverables
    ADD CONSTRAINT deliverables_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.actors(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.deliverables
    ADD CONSTRAINT deliverables_milestone_id_fkey FOREIGN KEY (milestone_id) REFERENCES public.milestones(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.deliverables
    ADD CONSTRAINT deliverables_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.deploy_shas
    ADD CONSTRAINT deploy_shas_main_id_fkey FOREIGN KEY (main_id) REFERENCES public.main_commits(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.deployments
    ADD CONSTRAINT deployments_artifact_id_fkey FOREIGN KEY (artifact_id) REFERENCES public.artifacts(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.doc_edge_versions
    ADD CONSTRAINT doc_edge_versions_doc_id_fkey FOREIGN KEY (doc_id) REFERENCES public.docs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.doc_edges
    ADD CONSTRAINT doc_edges_from_doc_fkey FOREIGN KEY (from_doc) REFERENCES public.docs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.doc_edges
    ADD CONSTRAINT doc_edges_owner_doc_fkey FOREIGN KEY (owner_doc) REFERENCES public.docs(id);

ALTER TABLE ONLY public.doc_edges
    ADD CONSTRAINT doc_edges_to_doc_fkey FOREIGN KEY (to_doc) REFERENCES public.docs(id);

ALTER TABLE ONLY public.doc_edges
    ADD CONSTRAINT doc_edges_to_rule_fkey FOREIGN KEY (to_rule) REFERENCES public.rules(id);

ALTER TABLE ONLY public.doc_notes
    ADD CONSTRAINT doc_notes_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.actors(id);

ALTER TABLE ONLY public.doc_notes
    ADD CONSTRAINT doc_notes_doc_id_fkey FOREIGN KEY (doc_id) REFERENCES public.docs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.doc_notes
    ADD CONSTRAINT doc_notes_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id);

ALTER TABLE ONLY public.doc_reviewers
    ADD CONSTRAINT doc_reviewers_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES public.actors(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.doc_reviewers
    ADD CONSTRAINT doc_reviewers_doc_id_fkey FOREIGN KEY (doc_id) REFERENCES public.docs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.doc_revision_edges
    ADD CONSTRAINT doc_revision_edges_doc_id_fkey FOREIGN KEY (doc_id) REFERENCES public.doc_revisions(doc_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.doc_revision_edges
    ADD CONSTRAINT doc_revision_edges_owner_doc_fkey FOREIGN KEY (owner_doc) REFERENCES public.docs(id);

ALTER TABLE ONLY public.doc_revision_edges
    ADD CONSTRAINT doc_revision_edges_to_doc_fkey FOREIGN KEY (to_doc) REFERENCES public.docs(id);

ALTER TABLE ONLY public.doc_revision_edges
    ADD CONSTRAINT doc_revision_edges_to_rule_fkey FOREIGN KEY (to_rule) REFERENCES public.rules(id);

ALTER TABLE ONLY public.doc_revisions
    ADD CONSTRAINT doc_revisions_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.actors(id);

ALTER TABLE ONLY public.doc_revisions
    ADD CONSTRAINT doc_revisions_doc_id_fkey FOREIGN KEY (doc_id) REFERENCES public.docs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.doc_rule_versions
    ADD CONSTRAINT doc_rule_versions_doc_id_fkey FOREIGN KEY (doc_id) REFERENCES public.docs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.doc_rule_versions
    ADD CONSTRAINT doc_rule_versions_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES public.rules(id);

ALTER TABLE ONLY public.doc_rule_versions
    ADD CONSTRAINT doc_rule_versions_rule_id_rule_version_fkey FOREIGN KEY (rule_id, rule_version) REFERENCES public.rule_versions(rule_id, version);

ALTER TABLE ONLY public.doc_rules
    ADD CONSTRAINT doc_rules_doc_id_fkey FOREIGN KEY (doc_id) REFERENCES public.docs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.doc_rules
    ADD CONSTRAINT doc_rules_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES public.rules(id);

ALTER TABLE ONLY public.doc_rules
    ADD CONSTRAINT doc_rules_rule_id_rule_version_fkey FOREIGN KEY (rule_id, rule_version) REFERENCES public.rule_versions(rule_id, version);

ALTER TABLE ONLY public.doc_sections
    ADD CONSTRAINT doc_sections_doc_id_fkey FOREIGN KEY (doc_id) REFERENCES public.docs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.doc_versions
    ADD CONSTRAINT doc_versions_doc_id_fkey FOREIGN KEY (doc_id) REFERENCES public.docs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.docs
    ADD CONSTRAINT docs_assignee_fkey FOREIGN KEY (owner) REFERENCES public.actors(id);

ALTER TABLE ONLY public.docs
    ADD CONSTRAINT docs_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.actors(id);

ALTER TABLE ONLY public.docs
    ADD CONSTRAINT docs_deleted_by_fkey FOREIGN KEY (deleted_by) REFERENCES public.actors(id);

ALTER TABLE ONLY public.docs
    ADD CONSTRAINT docs_generated_by_task_fkey FOREIGN KEY (generated_by_task) REFERENCES public.tasks(id);

ALTER TABLE ONLY public.docs
    ADD CONSTRAINT docs_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);

ALTER TABLE ONLY public.entity_edges
    ADD CONSTRAINT entity_edges_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.actors(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.env_deploys
    ADD CONSTRAINT env_deploys_flux_main_id_fkey FOREIGN KEY (flux_main_id) REFERENCES public.main_commits(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.env_deploys
    ADD CONSTRAINT env_deploys_gh_main_id_fkey FOREIGN KEY (gh_main_id) REFERENCES public.main_commits(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.github_user_tokens
    ADD CONSTRAINT github_user_tokens_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES public.actors(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.graph_projection_failures
    ADD CONSTRAINT graph_projection_failures_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.index_chunks
    ADD CONSTRAINT index_chunks_doc_id_fkey FOREIGN KEY (doc_id) REFERENCES public.docs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.index_chunks
    ADD CONSTRAINT index_chunks_project_fkey FOREIGN KEY (project) REFERENCES public.projects(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.index_chunks
    ADD CONSTRAINT index_chunks_skill_id_fkey FOREIGN KEY (skill_id) REFERENCES public.skills(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.index_chunks
    ADD CONSTRAINT index_chunks_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.issues
    ADD CONSTRAINT issues_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.leases
    ADD CONSTRAINT leases_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES public.actors(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.leases
    ADD CONSTRAINT leases_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.milestones
    ADD CONSTRAINT milestones_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.actors(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.milestones
    ADD CONSTRAINT milestones_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.project_daily_cost
    ADD CONSTRAINT project_daily_cost_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.project_daily_overhead_cost
    ADD CONSTRAINT project_daily_overhead_cost_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.project_entity_seq
    ADD CONSTRAINT project_entity_seq_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.project_overhead_usage
    ADD CONSTRAINT project_overhead_usage_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.project_participants
    ADD CONSTRAINT project_participants_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES public.actors(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.project_participants
    ADD CONSTRAINT project_participants_added_by_fkey FOREIGN KEY (added_by) REFERENCES public.actors(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.project_participants
    ADD CONSTRAINT project_participants_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.project_repos
    ADD CONSTRAINT project_repos_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.pull_requests
    ADD CONSTRAINT pull_requests_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.release_frontiers
    ADD CONSTRAINT release_frontiers_main_id_fkey FOREIGN KEY (main_id) REFERENCES public.main_commits(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.rule_edge_versions
    ADD CONSTRAINT rule_edge_versions_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES public.rules(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.rule_edge_versions
    ADD CONSTRAINT rule_edge_versions_to_rule_fkey FOREIGN KEY (to_rule) REFERENCES public.rules(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.rule_edges
    ADD CONSTRAINT rule_edges_from_rule_fkey FOREIGN KEY (from_rule) REFERENCES public.rules(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.rule_edges
    ADD CONSTRAINT rule_edges_to_rule_fkey FOREIGN KEY (to_rule) REFERENCES public.rules(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.rule_versions
    ADD CONSTRAINT rule_versions_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES public.rules(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.rules
    ADD CONSTRAINT rules_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);

ALTER TABLE ONLY public.runtime_events
    ADD CONSTRAINT runtime_events_artifact_id_fkey FOREIGN KEY (artifact_id) REFERENCES public.artifacts(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.skill_versions
    ADD CONSTRAINT skill_versions_skill_id_fkey FOREIGN KEY (skill_id) REFERENCES public.skills(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.skills
    ADD CONSTRAINT skills_latest_version_fk FOREIGN KEY (latest_version_id) REFERENCES public.skill_versions(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.state_log
    ADD CONSTRAINT state_log_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.task_activity
    ADD CONSTRAINT task_activity_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.task_blobs
    ADD CONSTRAINT task_blobs_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.actors(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.task_blobs
    ADD CONSTRAINT task_blobs_hash_fkey FOREIGN KEY (hash) REFERENCES public.blobs(hash) ON DELETE RESTRICT;

ALTER TABLE ONLY public.task_blobs
    ADD CONSTRAINT task_blobs_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.task_commits
    ADD CONSTRAINT task_commits_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.task_edges
    ADD CONSTRAINT task_edges_from_task_fkey FOREIGN KEY (from_task) REFERENCES public.tasks(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.task_edges
    ADD CONSTRAINT task_edges_to_task_fkey FOREIGN KEY (to_task) REFERENCES public.tasks(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.task_governed_by
    ADD CONSTRAINT task_governed_by_pinned_fkey FOREIGN KEY (rule_id, pinned_version) REFERENCES public.rule_versions(rule_id, version);

ALTER TABLE ONLY public.task_governed_by
    ADD CONSTRAINT task_governed_by_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES public.rules(id);

ALTER TABLE ONLY public.task_governed_by
    ADD CONSTRAINT task_governed_by_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.task_instructions
    ADD CONSTRAINT task_instructions_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.actors(id);

ALTER TABLE ONLY public.task_instructions
    ADD CONSTRAINT task_instructions_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_about_doc_fkey FOREIGN KEY (about_doc) REFERENCES public.docs(id);

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_assignee_fkey FOREIGN KEY (assignee) REFERENCES public.actors(id);

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.actors(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_deleted_by_fkey FOREIGN KEY (deleted_by) REFERENCES public.actors(id);

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_milestone_id_fkey FOREIGN KEY (milestone_id) REFERENCES public.milestones(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_plan_doc_fkey FOREIGN KEY (plan_doc) REFERENCES public.docs(id);

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.tokens
    ADD CONSTRAINT tokens_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES public.actors(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.tokens
    ADD CONSTRAINT tokens_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE CASCADE;

-- Seed rows.

INSERT INTO public.graph_projection (id, last_txid) VALUES
	(1, 0);

INSERT INTO public.model_prices (model, speed, effective_from, currency, input_micros, cache_write_5m_micros, cache_write_1h_micros, cache_read_micros, output_micros) VALUES
	('claude-opus-5', 'standard', '2000-01-01', 'USD', 5000000, 6250000, 10000000, 500000, 25000000),
	('claude-opus-4-8', 'standard', '2000-01-01', 'USD', 5000000, 6250000, 10000000, 500000, 25000000),
	('claude-opus-4-7', 'standard', '2000-01-01', 'USD', 5000000, 6250000, 10000000, 500000, 25000000),
	('claude-opus-4-6', 'standard', '2000-01-01', 'USD', 5000000, 6250000, 10000000, 500000, 25000000),
	('claude-opus-4-5', 'standard', '2000-01-01', 'USD', 5000000, 6250000, 10000000, 500000, 25000000),
	('claude-opus-4-1', 'standard', '2000-01-01', 'USD', 15000000, 18750000, 30000000, 1500000, 75000000),
	('claude-opus-5', 'fast', '2000-01-01', 'USD', 10000000, 12500000, 20000000, 1000000, 50000000),
	('claude-opus-4-8', 'fast', '2000-01-01', 'USD', 10000000, 12500000, 20000000, 1000000, 50000000),
	('claude-fable-5', 'standard', '2000-01-01', 'USD', 10000000, 12500000, 20000000, 1000000, 50000000),
	('claude-mythos-5', 'standard', '2000-01-01', 'USD', 10000000, 12500000, 20000000, 1000000, 50000000),
	('claude-sonnet-5', 'standard', '2000-01-01', 'USD', 2000000, 2500000, 4000000, 200000, 10000000),
	('claude-sonnet-5', 'standard', '2026-09-01', 'USD', 3000000, 3750000, 6000000, 300000, 15000000),
	('claude-sonnet-4-6', 'standard', '2000-01-01', 'USD', 3000000, 3750000, 6000000, 300000, 15000000),
	('claude-sonnet-4-5', 'standard', '2000-01-01', 'USD', 3000000, 3750000, 6000000, 300000, 15000000),
	('claude-haiku-4-5', 'standard', '2000-01-01', 'USD', 1000000, 1250000, 2000000, 100000, 5000000);

