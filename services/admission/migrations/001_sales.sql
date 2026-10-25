CREATE TABLE applied_sales (
 sale_id uuid PRIMARY KEY,
 product_id uuid NOT NULL,
 version bigint NOT NULL CHECK(version >= 1),
 configuration jsonb,
 allocation_digest text,
 per_shopper_unit_cap bigint CHECK(per_shopper_unit_cap BETWEEN 1 AND 9007199254740991),
 starts_at timestamptz,
 ends_at timestamptz,
 original_starts_at timestamptz,
 activated_version bigint,
 cancelled boolean NOT NULL DEFAULT false,
 CHECK(configuration IS NULL OR per_shopper_unit_cap IS NOT NULL)
);
-- Activation must not invent a configuration, even if it arrives first.
CREATE TABLE early_sale_activation (
 sale_id uuid NOT NULL,
 version bigint NOT NULL CHECK(version >= 1),
 allocation_digest text NOT NULL,
 configuration jsonb NOT NULL,
 PRIMARY KEY(sale_id,version)
);
