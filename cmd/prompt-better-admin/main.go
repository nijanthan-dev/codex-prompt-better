package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/recovery"
	"github.com/nijanthan-dev/codex-prompt-better/internal/retention"
	store "github.com/nijanthan-dev/codex-prompt-better/internal/store/postgres"
)

const databaseEnv = "PROMPT_BETTER_DATABASE_URL"

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("admin command is required")
	}
	switch args[0] {
	case "bootstrap-roles":
		return bootstrapRoles(ctx)
	case "migrate":
		return migrate(ctx)
	case "doctor":
		return doctor(ctx)
	case "retention-plan":
		return retentionPlan(ctx, args[1:])
	case "retention-apply":
		return retentionApply(ctx, args[1:])
	case "backup":
		return backup(ctx, args[1:])
	case "restore":
		return restore(ctx, args[1:])
	default:
		return errors.New("unknown admin command")
	}
}

func bootstrapRoles(ctx context.Context) error {
	db, err := migrationDB()
	if err != nil {
		return err
	}
	defer db.Close()
	if err := store.BootstrapRoles(ctx, db); err != nil {
		return errors.New("role bootstrap failed")
	}
	return printJSON(map[string]any{"status": "ready"})
}

func migrate(ctx context.Context) error {
	db, err := migrationDB()
	if err != nil {
		return err
	}
	defer db.Close()
	runner, err := store.NewRunner(db)
	if err != nil {
		return errors.New("migration setup failed")
	}
	if err := runner.Up(ctx); err != nil {
		return errors.New("migration failed")
	}
	version, err := runner.Version(ctx)
	if err != nil {
		return errors.New("schema version check failed")
	}
	return printJSON(map[string]any{"status": "ready", "schema_version": version})
}

func doctor(ctx context.Context) error {
	repo, err := repository(ctx)
	if err != nil {
		return err
	}
	defer repo.Close()
	result := repo.Doctor(ctx)
	if err := printJSON(result); err != nil {
		return err
	}
	if !result.Ready {
		return errors.New("storage is not ready")
	}
	return nil
}

func retentionPlan(ctx context.Context, args []string) error {
	options, err := parseRetention(args, false)
	if err != nil {
		return err
	}
	repo, err := repository(ctx)
	if err != nil {
		return err
	}
	defer repo.Close()
	plan, err := repo.PlanRetention(ctx, options.policyID, options.asOf, options.limit)
	if err != nil {
		return errors.New("retention planning failed")
	}
	return printJSON(map[string]any{"count": len(plan.Records),
		"digest": hex.EncodeToString(plan.Digest[:]), "as_of": plan.AsOf})
}

func retentionApply(ctx context.Context, args []string) error {
	options, err := parseRetention(args, true)
	if err != nil {
		return err
	}
	key, err := secretKey("PROMPT_BETTER_ARCHIVE_KEY_HEX")
	if err != nil {
		return err
	}
	repo, err := repository(ctx)
	if err != nil {
		return err
	}
	defer repo.Close()
	plan, err := repo.PlanRetention(ctx, options.policyID, options.asOf, options.limit)
	if err != nil {
		return errors.New("retention planning failed")
	}
	if len(plan.Records) == 0 {
		return printJSON(map[string]any{"status": "no_action", "count": 0})
	}
	batchID, err := randomUUID()
	if err != nil {
		return err
	}
	archiver, err := retention.NewFileArchiver(options.archiveDirectory,
		options.archiveKeyReference, key)
	if err != nil {
		return errors.New("archive setup failed")
	}
	receipt, err := archiver.Archive(ctx, batchID, plan)
	if err != nil {
		return errors.New("archive verification failed")
	}
	actionID, err := randomUUID()
	if err != nil {
		return err
	}
	auditID, err := randomUUID()
	if err != nil {
		return err
	}
	entityID, err := randomUUID()
	if err != nil {
		return err
	}
	count, err := repo.ApplyRetention(ctx, store.RetentionApply{
		ActionID: actionID, DeletionAuditID: auditID, ArchiveEntityID: entityID,
		Plan: plan, Receipt: receipt,
	})
	if err != nil {
		return errors.New("retention apply failed")
	}
	if err := repo.MaintainAfterRetention(ctx); err != nil {
		return errors.New("retention maintenance failed")
	}
	return printJSON(map[string]any{"status": "complete", "count": count,
		"archive_reference": receipt.ArchiveReference})
}

func backup(ctx context.Context, args []string) error {
	set := flag.NewFlagSet("backup", flag.ContinueOnError)
	database := set.String("database", "", "isolated database name")
	output := set.String("output", "", "encrypted backup file")
	if err := set.Parse(args); err != nil || *database == "" || *output == "" {
		return errors.New("backup requires --database and --output")
	}
	key, err := secretKey("PROMPT_BETTER_BACKUP_KEY_HEX")
	if err != nil {
		return err
	}
	metadata, err := recovery.DefaultToolchain().BackupFile(ctx, *database, *output, key, nil)
	if err != nil {
		return errors.New("encrypted backup failed")
	}
	return printJSON(map[string]any{"status": "complete",
		"plaintext_digest": hex.EncodeToString(metadata.PlaintextDigest[:]),
		"encrypted_digest": hex.EncodeToString(metadata.EncryptedDigest[:]),
		"plaintext_bytes":  metadata.PlaintextBytes})
}

func restore(ctx context.Context, args []string) error {
	set := flag.NewFlagSet("restore", flag.ContinueOnError)
	database := set.String("database", "", "new isolated database name")
	input := set.String("input", "", "encrypted backup file")
	if err := set.Parse(args); err != nil || *database == "" || *input == "" {
		return errors.New("restore requires --database and --input")
	}
	key, err := secretKey("PROMPT_BETTER_BACKUP_KEY_HEX")
	if err != nil {
		return err
	}
	if err := recovery.DefaultToolchain().RestoreFile(ctx, *database, *input, key, nil); err != nil {
		return errors.New("isolated restore failed")
	}
	return printJSON(map[string]any{"status": "restored"})
}

type retentionOptions struct {
	policyID            string
	asOf                time.Time
	limit               int
	archiveDirectory    string
	archiveKeyReference string
}

func parseRetention(args []string, apply bool) (retentionOptions, error) {
	set := flag.NewFlagSet("retention", flag.ContinueOnError)
	policy := set.String("policy", "", "retention policy UUID")
	asOfText := set.String("as-of", "", "UTC RFC3339 cutoff")
	limit := set.Int("limit", 1000, "bounded row count")
	directory := set.String("archive-directory", "", "encrypted archive directory")
	keyReference := set.String("archive-key-reference", "", "non-secret key reference")
	if err := set.Parse(args); err != nil || *policy == "" {
		return retentionOptions{}, errors.New("retention requires --policy")
	}
	asOf := time.Now().UTC()
	if *asOfText != "" {
		parsed, err := time.Parse(time.RFC3339, *asOfText)
		if err != nil {
			return retentionOptions{}, errors.New("invalid retention as-of time")
		}
		asOf = parsed.UTC()
	}
	if *limit < 1 || *limit > 1000 {
		return retentionOptions{}, errors.New("retention limit must be 1..1000")
	}
	if apply && (*directory == "" || *keyReference == "") {
		return retentionOptions{}, errors.New("apply requires archive directory and key reference")
	}
	return retentionOptions{policyID: *policy, asOf: asOf, limit: *limit,
		archiveDirectory: *directory, archiveKeyReference: *keyReference}, nil
}

func migrationDB() (*sql.DB, error) {
	dsn := os.Getenv(databaseEnv)
	if dsn == "" {
		return nil, errors.New("database reference is not configured")
	}
	db, err := store.OpenMigrationDB(dsn)
	if err != nil {
		return nil, errors.New("database setup failed")
	}
	return db, nil
}

func repository(ctx context.Context) (*store.Repository, error) {
	dsn := os.Getenv(databaseEnv)
	if dsn == "" {
		return nil, errors.New("database reference is not configured")
	}
	repo, err := store.OpenRepository(ctx, dsn, store.DefaultPoolConfig())
	if err != nil {
		return nil, errors.New("database is unavailable")
	}
	return repo, nil
}

func secretKey(name string) ([]byte, error) {
	encoded := os.Getenv(name)
	key, err := hex.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("encryption key is not configured")
	}
	return key, nil
}

func randomUUID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", errors.New("create internal identifier")
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
