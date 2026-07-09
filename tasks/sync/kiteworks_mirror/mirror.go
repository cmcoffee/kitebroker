package mirror

import (
	"fmt"
	"time"

	. "github.com/cmcoffee/kitebroker/core"
	"github.com/cmcoffee/kitebroker/tasks/migration/kiteworks"
)

func init() { RegisterTask(new(KW_MirrorTask)) }

// KW_MirrorTask copies a production Kiteworks server's users, folders,
// files, permissions, and SSH keys onto a hot-standby server. The persisted
// SyncState map records src→dst mappings as objects are copied.
//
// Each --run auto-selects between two paths:
//
//   - Full sync: a complete additive copy (optionally followed by a full-scan
//     --prune). Used on the first run, when scoped by --users/--src_profile,
//     when a periodic --full_sync_interval is due, or when the source activity
//     log no longer covers the window since the last run. Always authoritative.
//   - Differential sync: reads the source admin activity log since a persisted
//     cursor, refreshes only the affected users (adds/changes/member grants),
//     then reconciles deletions and membership removals. Cheap and incremental.
//
// The activity log only NOMINATES candidates; every deletion/removal is
// re-verified against the live source, so a wrong/missing event name degrades
// to a no-op and the full-sync path remains the correctness backstop. Combine
// with the global --repeat to run continuously: a full sync seeds state, then
// each interval does a fast differential pass.
type KW_MirrorTask struct {
	input struct {
		src_profile_name    string
		user_emails         []string
		no_ssh_keys         bool
		prune               bool
		run                 bool
		setup               bool
		full_sync_interval  time.Duration
		reconcile_page_size int
		dont_clone_profiles bool
	}
	state         *SyncState
	src_kw_db     Database
	src_kw_config Table
	src_admin     string
	SRC           KWAPI

	KiteBrokerTask
}

func (T KW_MirrorTask) Name() string {
	return "kiteworks_mirror"
}

func (T KW_MirrorTask) Desc() string {
	return "Sync: One-way mirror from a Kiteworks production server to a hot standby. --run auto-selects a full or differential (activity-log) sync; combine with --repeat for continuous mirroring."
}

func (T *KW_MirrorTask) Init() (err error) {
	T.src_kw_db = T.DB.Sub("kiteworks_mirror")
	// Source credentials live in a single shared store (see core.SourceConfig),
	// so a source configured via either the migration or mirror task is reused
	// and can't drift. Only the *config* is shared — the source token store and
	// SyncState stay in the mirror's own bucket (T.src_kw_db).
	T.src_kw_config = SourceConfig()
	T.src_admin = T.src_kw_config.GetString("src_admin")

	T.Flags.StringVar(&T.input.src_profile_name, "src_profile", "<Source Profile>", "Limit to users matching this source profile.")
	T.Flags.MultiVar(&T.input.user_emails, "users", "<user@domain.com>", "Limit to specific user(s).")
	T.Flags.BoolVar(&T.input.no_ssh_keys, "no_ssh_keys", "Do not copy SSH public keys.")
	T.Flags.BoolVar(&T.input.prune, "prune", "Force a full-scan mark-and-sweep of destination orphans this run. Not valid with --users or --src_profile.")
	T.Flags.BoolVar(&T.input.run, "run", "Perform the source→destination sync (auto-selects full or differential).")
	T.Flags.BoolVar(&T.input.setup, "setup", "Configure source Kiteworks connection.")
	T.Flags.DurationVar(&T.input.full_sync_interval, "full_sync_interval", 0, "Force a periodic full sync (copy + full-scan reconcile) at least this often. 0 = only when required.")
	T.Flags.IntVar(&T.input.reconcile_page_size, "reconcile_page_size", 1000, "Activity-log page size for the differential reconcile poll (1-1000).")
	T.Flags.BoolVar(&T.input.dont_clone_profiles, "dont_clone_profiles", "Do not clone custom user profiles onto the standby (cloning is on by default during a full sync).")
	T.Flags.Order("run", "prune", "full_sync_interval")
	if err := T.Flags.Parse(); err != nil {
		return err
	}

	if T.input.setup {
		return T.apiSetup()
	}

	if !T.testConfig() {
		fmt.Printf("Incomplete configuration found for source Kiteworks system...\n\n")
		T.apiSetup()
	}

	if !T.input.run {
		return fmt.Errorf("must specify --run (or --setup)")
	}

	if T.input.reconcile_page_size < 1 || T.input.reconcile_page_size > 1000 {
		return fmt.Errorf("--reconcile_page_size must be between 1 and 1000")
	}

	if T.input.prune {
		if len(T.input.user_emails) > 0 && !IsBlank(T.input.user_emails[0]) {
			return fmt.Errorf("--prune cannot be combined with --users (must be a full source scan)")
		}
		if !IsBlank(T.input.src_profile_name) {
			return fmt.Errorf("--prune cannot be combined with --src_profile (must be a full source scan)")
		}
	}

	return
}

// scoped reports whether the run is limited to a subset of users (via --users
// or --src_profile). A scoped run is not a full authoritative source scan, so
// it is never eligible for cursor-based differential mode.
func (T *KW_MirrorTask) scoped() bool {
	if len(T.input.user_emails) > 0 && !IsBlank(T.input.user_emails[0]) {
		return true
	}
	return !IsBlank(T.input.src_profile_name)
}

func (T *KW_MirrorTask) Main() (err error) {
	if err = T.configAPI(); err != nil {
		return err
	}

	T.state = NewSyncState(T.src_kw_db)

	PleaseWait.Set(func() string { return "Working ..." },
		[]string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()

	if T.differential() {
		return T.runDifferential()
	}
	return T.runFull()
}

// differential decides whether this run can use the cheap activity-log
// (differential) path or must fall back to a full sync. Full is required on
// first run, whenever a scoped subset (--users/--src_profile) or --prune is
// explicitly requested, when a periodic full sync is due, and when the source
// activity log no longer covers the window since our cursor.
func (T *KW_MirrorTask) differential() bool {
	if T.input.prune || T.scoped() {
		return false
	}
	cursor, ok := T.getCursor()
	if !ok {
		Log("mirror: no saved reconcile cursor; performing a full sync.")
		return false
	}
	if T.input.full_sync_interval > 0 {
		if last, ok := T.getTimeKey(lastFullSyncKey); !ok || time.Since(last) >= T.input.full_sync_interval {
			Log("mirror: periodic full sync due (interval %s); performing a full sync.", T.input.full_sync_interval)
			return false
		}
	}
	return T.differentialPossible(cursor)
}

// newCopier builds a copier over the source, optionally scoped to a specific
// set of user emails (used to limit the differential path to affected users).
// Profile cloning is honored only when clone is true — it's a full-sync
// concern, not something to repeat on every differential cycle.
func (T *KW_MirrorTask) newCopier(user_emails []string, clone bool) *kiteworks.KW_TO_KWTask {
	return kiteworks.NewCopier(&T.KiteBrokerTask, T.SRC, T.src_admin, kiteworks.CopyOptions{
		NoMail:            true,
		NoSshKeys:         T.input.no_ssh_keys,
		DstProfileName:    "Auto",
		SrcProfileName:    T.input.src_profile_name,
		UserEmails:        user_emails,
		CloneProfiles:     clone && !T.input.dont_clone_profiles,
		Observer:          T.state,
		DstFolderResolver: T.resolveDstFolder,
	})
}

// resolveDstFolder maps a source folder id to its known destination folder id
// using persisted sync state, so folder cloning targets the exact destination
// folder by id rather than by (ambiguous) name.
func (T *KW_MirrorTask) resolveDstFolder(src_folder_id string) (string, bool) {
	if T.state == nil {
		return "", false
	}
	m, ok := T.state.GetFolder(src_folder_id)
	if !ok || IsBlank(m.Dst_id) {
		return "", false
	}
	return m.Dst_id, true
}

// runFull performs the authoritative full sync: an unscoped (or explicitly
// scoped) additive copy followed, when requested, by the full-scan prune. It
// re-seeds the reconcile cursor from the run start so subsequent runs can go
// differential.
func (T *KW_MirrorTask) runFull() (err error) {
	Log("\n=== Full sync ===\n")
	run_start := time.Now()

	if err = T.newCopier(T.input.user_emails, true).RunCopy(); err != nil {
		return err
	}

	if T.input.prune {
		T.prune(run_start.Unix())
	}

	// A full scan is authoritative up to run_start; seed the cursor there so the
	// next run's differential window begins where this scan's knowledge ends.
	// A scoped run (--users/--src_profile) is NOT authoritative for the whole
	// server, so it must not advance the shared cursor or reset the full-sync
	// clock — otherwise it would suppress a needed full sync of everything else.
	if !T.scoped() {
		T.setCursor(run_start)
		T.setTimeKey(lastFullSyncKey, run_start)
	}
	T.setTimeKey(lastSuccessfulRunKey, run_start)
	return nil
}

// runDifferential applies only what changed since the cursor: a copy scoped to
// the users whose objects appear in the activity-log window (handles adds,
// re-uploads, and member grants), then the reconcile pass (handles deletions
// and membership removals). The cursor only advances on success.
func (T *KW_MirrorTask) runDifferential() (err error) {
	// A differential run is only reached when differential() confirmed a saved
	// cursor exists, so we always resume from exactly that cursor. If somehow it
	// is missing, fall back to a full sync rather than guessing a window.
	cursor, ok := T.getCursor()
	if !ok {
		Log("mirror: no saved cursor at differential start; performing a full sync.")
		return T.runFull()
	}

	Log("\n=== Differential sync (since %s) ===\n", cursor.Format(time.RFC3339))

	cs, err := T.pollActivities(cursor)
	if err != nil {
		return err
	}

	// Users needing a full copy: new/reactivated accounts and SSH-key changes
	// (their whole tree/keys are genuinely (re)provisioned by CopyUser).
	provision := cs.provisionEmails()
	provisioned := make(map[string]struct{}, len(provision))
	for _, e := range provision {
		provisioned[e] = struct{}{}
	}
	if len(provision) > 0 {
		Log("- Provisioning %d user(s) on destination.", len(provision))
		if err = T.newCopier(provision, false).RunCopy(); err != nil {
			return err
		}
	}

	// Content changes: process only the specific folders that changed, so
	// unchanged folders are never re-walked. Owners already fully provisioned
	// above are skipped (their tree was just walked).
	changed := cs.changedFoldersByOwner(provisioned)
	if len(changed) > 0 {
		n := 0
		for _, ids := range changed {
			n += len(ids)
		}
		Log("- Syncing %d changed folder(s) across %d user(s).", n, len(changed))
		if err = T.newCopier(nil, false).RunFolderCopy(changed); err != nil {
			return err
		}
	}

	T.reconcile(cs)

	// Advance one second past the newest processed activity so we don't
	// re-fetch it next cycle. If nothing newer was seen, keep the cursor.
	if cs.newest.After(cursor) {
		T.setCursor(cs.newest.Add(time.Second))
	}
	T.setTimeKey(lastSuccessfulRunKey, time.Now())
	return nil
}

func (T KW_MirrorTask) testAPI() bool {
	if !T.testConfig() {
		Err("API is missing some required configuration, please revisit '*** UNCONFIGURED ***' settings.")
		NeedInteract()
		return false
	}
	if err := T.configAPI(); err != nil {
		Err(err)
		return false
	}
	retry_count := T.SRC.Retries
	defer func() { T.SRC.Retries = retry_count }()
	T.SRC.Retries = 0
	T.SRC.TokenStore.Delete(T.src_admin)
	Flash("[%s]: Authenticating, please wait...", T.SRC.Server)
	if _, err := T.SRC.Session(T.src_admin).MyUser(); err != nil {
		Stdout("[ERROR] %s", err.Error())
		NeedInteract()
		return false
	}
	Log("[SUCCESS]: %s reports successful API communications!", T.SRC.Server)
	NeedInteract()
	return true
}

func (T *KW_MirrorTask) testConfig() bool {
	return SourceConfigComplete(T.src_kw_config)
}

func (T *KW_MirrorTask) configAPI() (err error) {
	T.SRC.APIClient = new(APIClient)
	T.SRC.APIClient.Server = T.src_kw_config.GetString("server")
	jwt := T.SRC.JWT()
	jwt.Issuer(T.src_kw_config.GetString("jwt_iss"))
	jwt.Key(T.src_kw_config.GetString("jwt_key"))
	jwt.UIDAttribute(T.src_kw_config.GetString("jwt_uid"))
	T.SRC.APIClient.ClientSecret(T.src_kw_config.GetString("client_secret"))
	T.SRC.APIClient.ApplicationID = T.src_kw_config.GetString("app_id")
	T.SRC.APIClient.RedirectURI = T.src_kw_config.GetString("redirect_uri")
	T.SRC.APIClient.ErrorScanner = T.KW.ErrorScanner
	T.SRC.Flags.Set(JWT_AUTH)
	// Bind token minting to the SOURCE's own KWAPI receiver so authentication
	// uses the source server, JWT config, and app credentials — not the
	// destination's. (T.KW.KWNewToken would mint against the destination.)
	T.SRC.APIClient.NewToken = T.SRC.KWNewToken
	T.SRC.ReaquireToken = true
	T.SRC.SetDatabase(T.src_kw_db)
	T.SRC.MaxChunkSize = T.KW.MaxChunkSize
	T.SRC.SetLimiter(T.KW.GetLimit())
	T.SRC.SetTransferLimiter(T.KW.GetTransferLimit())
	T.SRC.RequestTimeout = T.KW.RequestTimeout
	T.SRC.ConnectTimeout = T.KW.ConnectTimeout
	T.src_admin = T.src_kw_config.GetString("src_admin")
	return nil
}

func (T *KW_MirrorTask) apiSetup() (err error) {
	PleaseWait.Hide()
	defer PleaseWait.Show()
	var src_server, src_app_id, src_secret, src_redirect, jwt_key, jwt_uid, jwt_iss string
	T.src_kw_config.Get("jwt_key", &jwt_key)
	T.src_kw_config.Get("jwt_uid", &jwt_uid)
	T.src_kw_config.Get("jwt_iss", &jwt_iss)
	T.src_kw_config.Get("app_id", &src_app_id)
	T.src_kw_config.Get("client_secret", &src_secret)
	T.src_kw_config.Get("redirect_uri", &src_redirect)
	T.src_kw_config.Get("server", &src_server)
	T.src_kw_config.Get("src_admin", &T.src_admin)

	auth := NewOptions(" [Kiteworks JWT Configuration] ", "(selection or 'q' to return to previous)", 'q')
	auth.TextAreaVar(&jwt_key, "JWT RSA Private Key", "Paste JWT RSA Private Key Here...")
	auth.StringVar(&jwt_uid, "JWT UID Attribute", jwt_uid, "Please provide UID Attribute for JWT Claims.")
	auth.StringVar(&jwt_iss, "JWT Issuer", jwt_iss, "Please provide the Issuer (ISS) for JWT claims.")

	src_kw_auth := NewOptions("--- Source Kiteworks API Configuration ---", "(selection or 'q' to save & exit)", 'q')
	src_kw_auth.StringVar(&T.src_admin, "Source Kiteworks Admin Account", T.src_admin, "Please input the Kiteworks source's admin email")
	src_kw_auth.Options("Source Kiteworks JWT Settings", auth, false)
	src_kw_auth.StringVar(&src_server, "Source Kiteworks Server", src_server, "Please input the Kiteworks source host.")
	src_kw_auth.StringVar(&src_app_id, "Source Application ID", src_app_id, "Application ID for Kiteworks source.")
	src_kw_auth.SecretVar(&src_secret, "Source Client Secret", src_secret, "Client secret for Kiteworks source.")
	src_kw_auth.StringVar(&src_redirect, "Source Kiteworks Redirect URI", src_redirect, "Redirect URI for Kiteworks source.")
	if src_kw_auth.Select(false) {
		T.src_kw_config.Set("server", &src_server)
		T.src_kw_config.Set("src_admin", &T.src_admin)
		T.src_kw_config.Set("client_id", &src_app_id)
		T.src_kw_config.CryptSet("client_secret", &src_secret)
		T.src_kw_config.Set("app_id", &src_app_id)
		T.src_kw_config.Set("redirect_uri", &src_redirect)
		T.src_kw_config.CryptSet("jwt_key", &jwt_key)
		T.src_kw_config.CryptSet("jwt_uid", &jwt_uid)
		T.src_kw_config.CryptSet("jwt_iss", &jwt_iss)
	}
	Exit(0)
	return nil
}
