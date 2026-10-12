-- +goose Up
ALTER TABLE channels ADD COLUMN summary text NOT NULL DEFAULT '' CHECK (char_length(summary) <= 160);
ALTER TABLE channels ADD COLUMN description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 8000);
CREATE TABLE suppliers (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
    kind text NOT NULL CHECK (kind IN ('official', 'relay')),
    summary text NOT NULL DEFAULT '' CHECK (char_length(summary) <= 160),
    description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 8000),
    website_url text NOT NULL,
    docs_url text NOT NULL DEFAULT '',
    status_url text NOT NULL DEFAULT '',
    icon_data text NOT NULL DEFAULT '' CHECK (octet_length(icon_data) <= 350000),
    icon_source text NOT NULL DEFAULT '',
    visible boolean NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE supplier_endpoints (
    base_url text PRIMARY KEY,
    supplier_id uuid NOT NULL REFERENCES suppliers(id) ON DELETE CASCADE,
    label text NOT NULL DEFAULT '' CHECK (char_length(label) <= 64)
);
CREATE INDEX supplier_endpoints_supplier ON supplier_endpoints(supplier_id);

-- +goose Down
DROP TABLE supplier_endpoints;
DROP TABLE suppliers;
ALTER TABLE channels DROP COLUMN description, DROP COLUMN summary;
