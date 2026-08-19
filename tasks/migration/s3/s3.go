package s3

import (
	"fmt"
	"net/url"
	"strings"
	"sync"

	. "github.com/cmcoffee/kitebroker/core"
)

func init() { RegisterMigrationTask(new(S3MigrationTask)) }

// rootFallbackFolder is the Kiteworks folder that receives bucket-root objects
// (keys with no "/" prefix) when no --remote_kw_folder wrapper is configured.
// Root-level objects have no prefix to become a folder, and Kiteworks folder
// "0" is a virtual root that cannot receive uploads, so they land here.
const rootFallbackFolder = "S3 Import"

// S3MigrationTask imports objects from an S3 (or S3-compatible) bucket into a
// single Kiteworks destination folder owned by a specified user. The bucket's
// key prefixes are recreated as a Kiteworks folder tree, mirroring the folder
// handling used by the other migration tasks (Box, Quatrix, kw_to_kw).
type S3MigrationTask struct {
	KiteBrokerTask
	s3_db     Database
	s3_config Table
	cfg       S3Config
	src       *S3Source
	input     struct {
		owner           string
		dst             string
		overwrite_newer bool
		report          bool
	}
	folder_map  map[string]*KiteObject
	folder_lock sync.RWMutex

	FileCount   Tally
	FolderCount Tally
	Transferred Tally
	Skipped     Tally
}

// Name returns the task name used on the command line.
func (T *S3MigrationTask) Name() string {
	return "s3"
}

// Desc returns a human-readable description of the task.
func (T *S3MigrationTask) Desc() string {
	return "Bulk import files from an S3 (or S3-compatible) bucket into Kiteworks."
}

// filterInvalidChars escapes characters Kiteworks disallows in file/folder
// names so an S3 key that contains them still imports cleanly.
func filterInvalidChars(input string) string {
	var output []rune
	for _, v := range input {
		switch v {
		case '\\', '*', '?', '"', '<', '>', '|':
			output = append(output, []rune(url.QueryEscape(string(v)))...)
		default:
			output = append(output, v)
		}
	}
	return string(output)
}

// Init wires up flags, loads persisted config, and runs the interactive setup
// screen when requested or when no configuration exists yet.
func (T *S3MigrationTask) Init() (err error) {
	T.s3_db = T.DB.Sub("s3")
	T.s3_config = T.s3_db.Table("s3_config")

	// Load persisted connection settings.
	T.s3_config.Get("access_key", &T.cfg.AccessKey)
	T.s3_config.Get("secret_key", &T.cfg.SecretKey)
	T.s3_config.Get("region", &T.cfg.Region)
	T.s3_config.Get("bucket", &T.cfg.Bucket)
	T.s3_config.Get("prefix", &T.cfg.Prefix)
	T.s3_config.Get("endpoint", &T.cfg.Endpoint)

	setup := T.Flags.Bool("setup", "Configure S3 connection (access key, secret, region, bucket, endpoint).")
	T.Flags.StringVar(&T.input.owner, "owner", "<user@domain.com>", "Kiteworks user who will own the imported files/folders.")
	T.Flags.StringVar(&T.input.dst, "remote_kw_folder", NONE, "Optional wrapper folder; if unset, top-level S3 prefixes become top-level Kiteworks folders.")
	T.Flags.StringVar(&T.cfg.Prefix, "prefix", T.cfg.Prefix, "Only import objects under this S3 key prefix.")
	T.Flags.BoolVar(&T.input.overwrite_newer, "overwrite_newer", "Overwrite newer files on the server.")
	migrate := T.Flags.Bool("migrate", "Perform the actual import.")
	T.Flags.BoolVar(&T.input.report, "report", "List objects that would be imported without uploading (dry run).")
	T.Flags.Order("owner", "remote_kw_folder", "prefix", "overwrite_newer", "migrate", "report")
	if err = T.Flags.Parse(); err != nil {
		return err
	}

	if *setup || !T.configComplete() {
		T.runSetup()
		Exit(0)
	}

	if *migrate && T.input.report {
		return fmt.Errorf("--migrate and --report are mutually exclusive, please specify only one")
	}
	if !*migrate && !T.input.report {
		return fmt.Errorf("must specify --migrate or --report (or --setup to configure the connection)")
	}
	if *migrate && IsBlank(T.input.owner) {
		return fmt.Errorf("must specify --owner (the Kiteworks user to own imported files)")
	}

	return nil
}

// configComplete reports whether the minimum required settings are present.
func (T *S3MigrationTask) configComplete() bool {
	return !IsBlank(T.cfg.AccessKey, T.cfg.SecretKey, T.cfg.Bucket) &&
		(!IsBlank(T.cfg.Region) || !IsBlank(T.cfg.Endpoint))
}

// runSetup presents the interactive configuration screen and persists the
// result, encrypting the secret key.
func (T *S3MigrationTask) runSetup() {
	opts := NewOptions("--- S3 Connection Configuration ---", "(selection or 'q' to save & exit)", 'q')
	opts.StringVar(&T.cfg.AccessKey, "Access Key ID", T.cfg.AccessKey, "S3 access key ID.")
	opts.SecretVar(&T.cfg.SecretKey, "Secret Access Key", T.cfg.SecretKey, "S3 secret access key.")
	opts.StringVar(&T.cfg.Region, "Region", T.cfg.Region, "S3 region, e.g. us-east-1.")
	opts.StringVar(&T.cfg.Bucket, "Bucket", T.cfg.Bucket, "S3 bucket name.")
	opts.StringVar(&T.cfg.Prefix, "Prefix", T.cfg.Prefix, "Optional key prefix to limit the import.")
	opts.StringVar(&T.cfg.Endpoint, "Endpoint (optional)", T.cfg.Endpoint, "Custom endpoint for MinIO/Wasabi/other S3-compatible stores.")

	if opts.Select(false) {
		T.s3_config.Set("access_key", T.cfg.AccessKey)
		T.s3_config.CryptSet("secret_key", T.cfg.SecretKey)
		T.s3_config.Set("region", T.cfg.Region)
		T.s3_config.Set("bucket", T.cfg.Bucket)
		T.s3_config.Set("prefix", T.cfg.Prefix)
		T.s3_config.Set("endpoint", T.cfg.Endpoint)
	}
}

// resolvePath resolves (creating as needed) a Kiteworks folder path relative to
// the destination root, caching results to avoid re-resolving shared prefixes.
func (T *S3MigrationTask) resolvePath(path string) (*KiteObject, error) {
	path = filterInvalidChars(path)

	T.folder_lock.RLock()
	if folder, found := T.folder_map[path]; found {
		T.folder_lock.RUnlock()
		return folder, nil
	}
	T.folder_lock.RUnlock()

	folder, err := T.KW.Session(T.input.owner).Folder("0").ResolvePath(path)
	if err != nil {
		return nil, fmt.Errorf("Error resolving path '%s': %v", path, err)
	}

	T.folder_lock.Lock()
	T.folder_map[path] = &folder
	T.folder_lock.Unlock()
	return &folder, nil
}

// Main enumerates the bucket and imports each object into Kiteworks, mapping S3
// key prefixes onto a Kiteworks folder tree under the destination folder.
func (T *S3MigrationTask) Main() (err error) {
	if T.input.report {
		return T.RunReport()
	}

	T.src, err = NewS3Source(T.cfg, T.KW.ProxyURI)
	if err != nil {
		return err
	}

	// Verify the owner exists on Kiteworks before we start transferring.
	if _, err = T.KW.Admin().FindUser(T.input.owner); err != nil {
		return fmt.Errorf("Error looking up owner '%s': %v", T.input.owner, err)
	}

	T.folder_map = make(map[string]*KiteObject)
	T.FileCount = T.Report.Tally("Imported Files")
	T.FolderCount = T.Report.Tally("Imported Folders")
	T.Transferred = T.Report.Tally("Data Transferred", HumanSize)
	T.Skipped = T.Report.Tally("Skipped Files")

	// When a wrapper folder is set, establish it up-front so its creation is
	// not racing across the upload workers. Without a wrapper, top-level S3
	// prefixes become top-level Kiteworks folders and are resolved on demand.
	if !IsBlank(T.input.dst) {
		if _, err = T.resolvePath(T.input.dst); err != nil {
			return err
		}
	}

	message := func() string {
		return fmt.Sprintf("Working .. [ Folders: %d | Files: %d (%s) ]", T.FolderCount.Value(), T.FileCount.Value(), HumanSize(T.Transferred.Value()))
	}
	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()

	dst_label := T.input.dst
	if IsBlank(dst_label) {
		dst_label = "<top-level prefixes>"
	}
	Log("Starting S3 import from bucket '%s' (prefix: '%s') -> Kiteworks:/%s (owner: %s)...", T.cfg.Bucket, T.cfg.Prefix, dst_label, T.input.owner)

	wg := NewLimitGroup(25)
	err = T.src.ListObjects(T.cfg.Prefix, func(obj S3Object) error {
		wg.Add(1)
		go func(o S3Object) {
			defer wg.Done()
			if uploadErr := T.importObject(o); uploadErr != nil {
				Err("%s: %v", o.Key, uploadErr)
			}
		}(obj)
		return nil
	})
	wg.Wait()

	if err != nil {
		return err
	}
	Log("\n=== S3 Import Complete ===")
	return nil
}

// importObject maps a single S3 object's key to a Kiteworks folder path and
// uploads it, streaming directly from S3 without staging to disk.
//
// By default (no --remote_kw_folder) the object key's leading prefix segments
// become top-level Kiteworks folders. When a wrapper folder is configured, the
// whole tree is nested under it instead.
func (T *S3MigrationTask) importObject(obj S3Object) error {
	folder_path, name := destFolderPath(T.input.dst, obj.Key)
	T.FolderCount.Add(1)

	kwFolder, err := T.resolvePath(folder_path)
	if err != nil {
		return err
	}

	src := TransferCounter(T.src.Download(obj.Key, obj.Size), T.Transferred.Add)
	file, err := T.KW.Session(T.input.owner).Upload(filterInvalidChars(name), obj.Size, obj.Modified, T.input.overwrite_newer, true, true, *kwFolder, src)
	if err != nil {
		if IsAPIError(err, "ERR_ENTITY_EXISTS") {
			T.Skipped.Add(1)
			return nil
		}
		return fmt.Errorf("upload failed: %v", err)
	}
	if file != nil {
		T.FileCount.Add(1)
	}
	return nil
}

// splitKey separates an S3 object key into its directory portion (using "/" as
// the delimiter, which matches Kiteworks' SplitPath) and the trailing file name.
func splitKey(key string) (dir, name string) {
	idx := strings.LastIndex(key, "/")
	if idx < 0 {
		return NONE, key
	}
	return key[:idx], key[idx+1:]
}

// destFolderPath computes the Kiteworks destination folder path and file name
// for an S3 object key. This is the single source of truth shared by the actual
// import and the --report preview, so a dry run always matches the real run.
//
//   - With a wrapper folder: <wrapper>/<key-dir>, or just <wrapper> for a
//     bucket-root object.
//   - Without a wrapper: <key-dir> (top-level prefixes become top-level
//     folders), or rootFallbackFolder for a bucket-root object that would
//     otherwise have no folder to live in.
func destFolderPath(wrapper, key string) (folder_path, name string) {
	dir, name := splitKey(key)
	switch {
	case IsBlank(wrapper) && IsBlank(dir):
		folder_path = rootFallbackFolder
	case IsBlank(wrapper):
		folder_path = dir
	case IsBlank(dir):
		folder_path = wrapper
	default:
		folder_path = fmt.Sprintf("%s/%s", wrapper, dir)
	}
	return folder_path, name
}
