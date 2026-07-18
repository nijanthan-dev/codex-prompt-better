DO $roles$
DECLARE
    role_name text;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('prompt-better-role-bootstrap'));
    FOREACH role_name IN ARRAY ARRAY[
        'prompt_better_migrator',
        'prompt_better_runtime',
        'prompt_better_collector',
        'prompt_better_reporter'
    ] LOOP
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = role_name) THEN
            EXECUTE format(
                'CREATE ROLE %I NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS',
                role_name
            );
        END IF;
    END LOOP;
END
$roles$;

DO $database$
BEGIN
    EXECUTE format('REVOKE ALL ON DATABASE %I FROM PUBLIC', current_database());
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO prompt_better_migrator,prompt_better_runtime,prompt_better_collector,prompt_better_reporter', current_database());
END
$database$;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
