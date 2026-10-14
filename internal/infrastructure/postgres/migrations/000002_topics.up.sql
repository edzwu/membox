-- 000002_topics.up.sql — topics as first-class collections (ADR discussion:
-- topic = the collection concept; documents/issues attach via relation
-- tables, entities stay standalone so they can span topics and survive
-- topic deletion).

CREATE TABLE topics (
    id          UUID PRIMARY KEY,
    owner_id    UUID NOT NULL REFERENCES users(id),
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ
);
CREATE INDEX topics_owner_listing ON topics(owner_id, deleted_at, updated_at DESC, id);

-- Issues are standalone entities (mirrors the local questions store: status
-- enum identical, document anchor optional). They never inherit permissions
-- through a join — owner_id lives on the entity.
CREATE TABLE issues (
    id          UUID PRIMARY KEY,
    owner_id    UUID NOT NULL REFERENCES users(id),
    document_id UUID REFERENCES documents(id),
    title       TEXT NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'open'
                CHECK (status IN ('open','answered','archived')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ
);
CREATE INDEX issues_owner_listing ON issues(owner_id, status, updated_at DESC, id);

CREATE TABLE topic_members (
    topic_id    UUID NOT NULL REFERENCES topics(id),
    document_id UUID NOT NULL REFERENCES documents(id),
    added_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (topic_id, document_id)
);

CREATE TABLE topic_issues (
    topic_id UUID NOT NULL REFERENCES topics(id),
    issue_id UUID NOT NULL REFERENCES issues(id),
    position INTEGER NOT NULL DEFAULT 0,
    added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (topic_id, issue_id)
);
