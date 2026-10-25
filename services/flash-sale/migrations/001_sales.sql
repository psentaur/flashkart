-- btree_gist is enabled by the Flash Sale database owner, never by peer services.
CREATE TABLE sales (
 sale_id uuid PRIMARY KEY,
 product_id uuid NOT NULL,
 version bigint NOT NULL CHECK(version >= 1),
 document jsonb NOT NULL,
 creation_digest text NOT NULL,
 original_starts_at timestamptz NOT NULL,
 starts_at timestamptz NOT NULL,
 ends_at timestamptz NOT NULL,
 allocated_units bigint NOT NULL CHECK(allocated_units > 0),
 unit_price_minor bigint NOT NULL CHECK(unit_price_minor BETWEEN 1 AND 9007199254740991),
 per_shopper_unit_cap bigint NOT NULL CHECK(per_shopper_unit_cap BETWEEN 1 AND 9007199254740991),
 configured_by uuid NOT NULL,
 configured_at timestamptz NOT NULL,
 cancelled_at timestamptz,
 frozen_at timestamptz,
 activated_at timestamptz,
 CHECK(starts_at < ends_at),
 EXCLUDE USING gist (product_id WITH =, tstzrange(starts_at,ends_at,'[)') WITH &&) WHERE (cancelled_at IS NULL)
);
CREATE TABLE sale_readiness (
 sale_id uuid NOT NULL REFERENCES sales(sale_id),
 version bigint NOT NULL CHECK(version >= 1),
 participant text NOT NULL CHECK(participant IN ('catalog','admission','inventory')),
 allocation_digest text NOT NULL,
 status text NOT NULL CHECK(status IN ('READY','FAILED')),
 error_code text,
 applied_at timestamptz NOT NULL,
 PRIMARY KEY(sale_id,version,participant)
);
