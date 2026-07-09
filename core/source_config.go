package core

// Source Kiteworks connection settings (the "source" side of a migration or a
// mirror) are stored in a single shared bucket so a source configured once via
// either task is visible to both — one source of truth, no drift.
//
// Only the connection *config* is shared. Each task keeps its own token store
// and task state in its own bucket, so sharing config never crosses tokens or
// sync state between tasks.

const (
	// sharedSourceBucket is the canonical location for source connection config.
	sharedSourceBucket = "kiteworks_source"
	// legacyMigrationTaskBucket / legacyMigrationSubBucket locate the migration
	// task's original config: global.db.Bucket("kiteworks")/kiteworks_migration.
	// (Migration tasks are rooted via Bucket(<task name>); the migration task's
	// name is "kiteworks".) This fixed path is adopted once so an existing
	// configuration keeps working without re-entry, regardless of which task
	// runs first.
	legacyMigrationTaskBucket = "kiteworks"
	legacyMigrationSubBucket  = "kiteworks_migration"
	sourceConfigTable         = "src_kw_config"
)

// globalDB is the process-wide database root, set once at startup via
// SetGlobalDB. The shared source config is anchored here rather than at a task's
// T.DB, because T.DB differs between task types (migration tasks and normal
// tasks are rooted differently) — anchoring per-task would put each task's
// "shared" config in a different place. A fixed root guarantees one location.
var globalDB Database

// SetGlobalDB registers the process-wide database root. Call once at startup,
// right after the main database is opened.
func SetGlobalDB(db Database) { globalDB = db }

// requiredSourceConfigKeys are the values that must all be present for a source
// connection to be considered fully configured.
var requiredSourceConfigKeys = []string{
	"jwt_key", "jwt_uid", "jwt_iss", "app_id", "client_secret", "redirect_uri", "server", "src_admin",
}

// encryptedSourceConfigKeys are stored via CryptSet; the rest via Set. Keep this
// consistent with how the setup screens persist each value.
var encryptedSourceConfigKeys = map[string]bool{
	"jwt_key": true, "jwt_uid": true, "jwt_iss": true, "client_secret": true,
}

// SourceConfigComplete reports whether a config table holds every required key.
func SourceConfigComplete(cfg Table) bool {
	for _, v := range requiredSourceConfigKeys {
		if cfg.GetString(v) == NONE {
			return false
		}
	}
	return true
}

// SourceConfig returns the shared source-connection config table, anchored at
// the process-wide database root so every task resolves the same location
// regardless of task type. On first use, if the shared bucket is not yet
// configured but the migration task's original config bucket holds a complete
// config, that config is copied forward so both tasks adopt it without re-entry.
func SourceConfig() Table {
	if globalDB == nil {
		// Should never happen in normal startup; avoids a nil-deref if a task
		// runs before SetGlobalDB.
		Fatal("SourceConfig: global database root not initialized.")
	}
	shared := globalDB.Sub(sharedSourceBucket).Table(sourceConfigTable)
	if SourceConfigComplete(shared) {
		return shared
	}
	legacy := globalDB.Bucket(legacyMigrationTaskBucket).Sub(legacyMigrationSubBucket).Table(sourceConfigTable)
	if SourceConfigComplete(legacy) {
		Log("Adopting existing source configuration from '%s/%s' into shared store '%s'.", legacyMigrationTaskBucket, legacyMigrationSubBucket, sharedSourceBucket)
		copySourceConfig(legacy, shared)
		return shared
	}
	return shared
}

// copySourceConfig copies every known source-config key from src to dst,
// preserving each key's encrypted/plaintext storage.
func copySourceConfig(src, dst Table) {
	keys := append([]string{"client_id"}, requiredSourceConfigKeys...)
	for _, k := range keys {
		v := src.GetString(k)
		if v == NONE {
			continue
		}
		if encryptedSourceConfigKeys[k] {
			dst.CryptSet(k, &v)
		} else {
			dst.Set(k, &v)
		}
	}
}
