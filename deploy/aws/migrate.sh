#!/bin/sh
set -eu
export PGUSER=flashkart_admin PGDATABASE=flashkart PGSSLMODE=verify-full PGSSLROOTCERT=/rds/ca.pem
for backend in a b c d transactional; do
    export PGHOST=$(jq -er --arg key "$backend" '.[$key].host' /sql/databases.json)
    secret=$(jq -er --arg key "$backend" '.[$key].admin_secret' /sql/databases.json)
    PGPASSWORD=$(aws secretsmanager get-secret-value --secret-id "$secret" --query SecretString --output text | jq -er .password)
    export PGPASSWORD
    if [ "$backend" = transactional ]; then
        group=transactional
        roles='flash_sale_app inventory_app admission_app orders_app payment_app'
    else
        group=replica
        roles=replica_app
    fi
    for role in $roles; do
        key=$role
        if [ "$group" = replica ]; then key=$backend; fi
        password=$(cat "/database/$key")
        psql -X -v ON_ERROR_STOP=1 -v role="$role" -v password="$password" <<'SQL'
BEGIN;
SELECT pg_advisory_xact_lock(1);
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'role', :'password')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'role') \gexec
SELECT format('GRANT %I TO flashkart_admin', :'role') \gexec
SELECT format('ALTER ROLE %I PASSWORD %L', :'role', :'password') \gexec
COMMIT;
SQL
    done
    for file in /sql/"$group"-*.sql; do psql -X -v ON_ERROR_STOP=1 -f "$file"; done
done
