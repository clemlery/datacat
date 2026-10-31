CREATE TABLE datasets (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    name        text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    description text        CHECK (char_length(description) <= 5000),
    source_url  text,
    format      text        NOT NULL CHECK (format IN ('csv', 'json', 'parquet', 'xlsx', 'xml', 'geojson', 'other')),
    size_bytes  bigint      CHECK (size_bytes >= 0),
    row_count   bigint      CHECK (row_count >= 0),
    owner       text        CHECK (char_length(owner) <= 200),
    license     text        CHECK (char_length(license) <= 100),
    created_at  timestamptz NOT NULL DEFAULT date_trunc('second', now()),
    updated_at  timestamptz NOT NULL DEFAULT date_trunc('second', now()),
    deleted_at  timestamptz
);

-- Non partiel : le nom d'un dataset supprimé reste réservé à vie.
CREATE UNIQUE INDEX datasets_name_lower_key ON datasets (lower(name));

-- Correspond exactement à l'ordre du listing.
CREATE INDEX datasets_created_at_id_idx ON datasets (created_at DESC, id DESC);

CREATE TABLE tags (
    id   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text   NOT NULL UNIQUE CHECK (char_length(name) BETWEEN 1 AND 50)
);

CREATE TABLE dataset_tags (
    dataset_id uuid   NOT NULL REFERENCES datasets (id) ON DELETE CASCADE,
    tag_id     bigint NOT NULL REFERENCES tags (id) ON DELETE RESTRICT,
    PRIMARY KEY (dataset_id, tag_id)
);

CREATE INDEX dataset_tags_tag_id_idx ON dataset_tags (tag_id);
