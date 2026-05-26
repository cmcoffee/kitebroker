package mirror

import (
	"fmt"
	"time"

	. "github.com/cmcoffee/kitebroker/core"
	"github.com/cmcoffee/kitebroker/tasks/migration/kiteworks"
)

func init() { RegisterTask(new(KW_MirrorTask)) }

// KW_MirrorTask copies a production Kiteworks server's users, folders,
// files, permissions, and SSH keys onto a hot-standby server. Each run
// performs a full additive copy: anything new on source is created on
// destination, anything already in sync is skipped. The persisted state
// map records src→dst mappings as objects are copied — that's the
// foundation a future webhook + reconcile layer will build on.
//
// WIP: this task currently provides only periodic --run copy (with
// optional --prune). Webhook-driven live sync is not yet implemented,
// so the "mirror" is only as fresh as the most recent --run.
type KW_MirrorTask struct {
	input struct {
		src_profile_name string
		user_emails      []string
		no_ssh_keys      bool
		prune            bool
		run              bool
		setup            bool
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
	return "Sync: [WIP] One-way mirror from a Kiteworks production server to a hot standby. Periodic --run copy works; live sync not yet implemented."
}

func (T *KW_MirrorTask) Init() (err error) {
	T.src_kw_db = T.DB.Sub("kiteworks_mirror")
	T.src_kw_config = T.src_kw_db.Table("src_kw_config")
	T.src_admin = T.src_kw_config.GetString("src_admin")

	T.Flags.StringVar(&T.input.src_profile_name, "src_profile", "<Source Profile>", "Limit to users matching this source profile.")
	T.Flags.MultiVar(&T.input.user_emails, "users", "<user@domain.com>", "Limit to specific user(s).")
	T.Flags.BoolVar(&T.input.no_ssh_keys, "no_ssh_keys", "Do not copy SSH public keys.")
	T.Flags.BoolVar(&T.input.prune, "prune", "After copy, delete files/folders/perms/SSH keys on destination that no longer exist on source. Not valid with --users or --src_profile.")
	T.Flags.BoolVar(&T.input.run, "run", "Perform the source→destination copy.")
	T.Flags.BoolVar(&T.input.setup, "setup", "Configure source Kiteworks connection.")
	T.Flags.Order("run", "prune")
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

func (T *KW_MirrorTask) Main() (err error) {
	if err = T.configAPI(); err != nil {
		return err
	}

	T.state = NewSyncState(T.src_kw_db)

	PleaseWait.Set(func() string { return "Working ..." },
		[]string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()

	copier := kiteworks.NewCopier(&T.KiteBrokerTask, T.SRC, T.src_admin, kiteworks.CopyOptions{
		NoMail:         true,
		NoSshKeys:      T.input.no_ssh_keys,
		DstProfileName: "Auto",
		SrcProfileName: T.input.src_profile_name,
		UserEmails:     T.input.user_emails,
		Observer:       T.state,
	})

	run_start_ts := time.Now().Unix()
	if err = copier.RunCopy(); err != nil {
		return err
	}

	if T.input.prune {
		T.prune(run_start_ts)
	}
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
	for _, v := range []string{"jwt_key", "jwt_uid", "jwt_iss", "app_id", "client_secret", "redirect_uri", "server", "admin"} {
		if x := T.src_kw_config.GetString(v); x == NONE {
			return false
		}
	}
	return true
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
	T.SRC.APIClient.NewToken = T.KW.KWNewToken
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
