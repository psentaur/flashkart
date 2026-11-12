#!/bin/sh
set -eu

wait_for_db() {
    attempts=0
    until "$@" exec -T "$database" psql -U flashkart_admin -d flashkart -Atqc 'SELECT 1' >/dev/null 2>&1; do
        attempts=$((attempts + 1))
        if [ "$attempts" -ge 30 ]; then
            echo "PostgreSQL startup timed out for $database" >&2
            return 1
        fi
        sleep 1
    done
}

for id in a b c d; do
    database="postgres-$id"
    wait_for_db "$@"
    {
        cat <<'SQL'
BEGIN;
SELECT pg_advisory_xact_lock(1);
SELECT format('CREATE ROLE replica_app LOGIN PASSWORD %L', :'app_password')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'replica_app') \gexec
COMMIT;
SQL
        cat migrations/replica/*.sql
    } | "$@" exec -T "$database" psql -X -v ON_ERROR_STOP=1 -U flashkart_admin -d flashkart -v "app_password=flashkart-local-$id"
done

database=postgres-transactional
wait_for_db "$@"
{
    cat <<'SQL'
BEGIN;
SELECT pg_advisory_xact_lock(1);
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', role_name, 'flashkart-local-' || role_name)
FROM unnest(ARRAY['flash_sale_app','admission_app','inventory_app','orders_app','payment_app']) AS role_name
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = role_name) \gexec
COMMIT;
SQL
    cat migrations/transactional/*.sql
} | "$@" exec -T "$database" psql -X -v ON_ERROR_STOP=1 -U flashkart_admin -d flashkart
