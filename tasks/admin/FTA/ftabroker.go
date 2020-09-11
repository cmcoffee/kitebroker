package FTA

import (
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"strings"
	"sync"
)

// Object for task.
type Broker struct {
	api           *FTAClient
	ppt           Passport
	user          string
	cache         Database
	workspaces    []string
	upload_wg     LimitGroup
	perm_map_lock sync.Mutex
	perm_map      map[int]map[string]int
}

// Task objects need to be able create a new copy of themself.
func (T *Broker) New() Task {
	return new(Broker)
}

// Task init function, should parse flag, do pre-checks.
func (T *Broker) Init(flag *FlagSet) (err error) {
	server := flag.String("server", "<FTA Server>", "Server for FTA System")
	client_id := flag.String("client_id", "<client_id>", "Client ID for FTA API.")
	secret_key := flag.String("secret_key", "<secret_key>", "Secret Key for FTA API.")
	signature_key := flag.String("signature_key", "<signature_key>", "Signature key for FTA API.")
	redirect_uri := flag.String("redirect_uri", "https://kitebroker/", "Redirect URI for FTA API.")
	flag.StringVar(&T.user, "user", "<FTA User>", "FTA User Account.")
	flag.SplitVar(&T.workspaces, "workspace", "<FTA Workspace>", "FTA Workspaces to migrate, leave blank for all.")
	file := flag.String("file", "<FTA config file>", "Configuration file for FTA API.")
	err = flag.Parse()
	if err != nil {
		return err
	}

	var c ConfigStore
	if !IsBlank(*file) {
		if err := c.File(*file); err != nil {
			return err
		}
	}

	load_setting := func(name string, val *string) (err error) {
		if x := c.Get("fta_api", name); !IsBlank(x) {
			*val = x
		}
		if IsBlank(*val) {
			return fmt.Errorf("--%s is a required parameter.", name)
		}
		return nil
	}

	if err := load_setting("server", server); err != nil {
		return err
	}
	if err := load_setting("client_id", client_id); err != nil {
		return err
	}
	if err := load_setting("secret_key", secret_key); err != nil {
		return err
	}
	if err := load_setting("signature_key", signature_key); err != nil {
		return err
	}
	if err := load_setting("redirect_uri", redirect_uri); err != nil {
		return err
	}

	if IsBlank(T.user) {
		return fmt.Errorf("--user is a required paramter.")
	}

	T.api = &FTAClient{new(APIClient)}
	T.api.Server = *server
	T.api.ApplicationID = *client_id
	T.api.ClientSecret(*secret_key)
	T.api.Signature(*signature_key)
	T.api.VerifySSL = false
	T.api.RedirectURI = *redirect_uri

	return
}

// Main function, Passport hands off KWAPI Session, a Database and a TaskReport object.
func (T *Broker) Main(passport Passport) (err error) {
	T.ppt = passport
	T.cache = OpenCache()

	T.api.TokenStore = KVLiteStore(T.ppt.SubStore)
	T.api.Retries = T.ppt.Retries
	T.api.Snoop = T.ppt.Snoop
	T.api.ProxyURI = T.ppt.ProxyURI
	T.api.AgentString = T.ppt.AgentString
	T.api.RequestTimeout = T.ppt.RequestTimeout
	T.api.ConnectTimeout = T.ppt.ConnectTimeout
	T.api.APIClient.NewToken = T.api.newFTAToken
	T.api.ErrorScanner = T.api.ftaError
	T.api.TokenErrorCodes = []string{"221", "120", "ERR_AUTH_UNAUTHORIZED", "INVALID_GRANT"}

	T.upload_wg = NewLimitGroup(10)
	T.perm_map = make(map[int]map[string]int)

	sess := T.api.Session(T.user)

	root, err := sess.Workspace(NONE).Children()
	if err != nil {
		return err
	}

	var (
		verified_ws  []string
		s_ws         []string
		specified_ws bool
	)

	for _, v := range T.workspaces {
		s_ws = append(s_ws, v)
	}

	for _, v := range root {
		if v.ParentID != "0" {
			continue
		}
		if v.Type == "d" {
			if len(s_ws) > 0 {
				specified_ws = true
				for i := 0; i < len(s_ws); i++ {
					if SplitPath(s_ws[i])[0] == v.Name {
						verified_ws = append(verified_ws, s_ws[i])
						s_ws = append(s_ws[:i], s_ws[i+1:]...)
					}
				}
				if len(s_ws) == 0 {
					break
				}
			} else {
				verified_ws = append(verified_ws, v.Name)
			}
		}
	}

	for _, v := range s_ws {
		Err("%s: Unable to process as it is not a valid top-level workspace.", v)
	}

	T.user = strings.ToLower(T.user)

	kw_user_map := make(map[string]string)

	for _, v := range verified_ws {
		source, err := sess.Workspace(NONE).Find(v)
		if err != nil {
			Err("%s: %v", v, err)
			continue
		}
		source.full_path = v
		ws_users, err := sess.Workspace(source.ID).Users()
		if err != nil {
			if IsAPIError(err, "222") {
				fail_msg := fmt.Sprintf("%s: Insufficient permissions required to duplicate workspace.", v)
				if !specified_ws {
					Warn(fail_msg)
				} else {
					Err(fail_msg)
				}
				continue
			} else {
				Err("%s: %v", v, err)
				continue
			}
		}

		fta_user_map := make(map[string]int)
		for _, v := range ws_users {
			fta_user_map[v.Name] = v.UserType
		}

		var kw_user string

		fta_user := T.user
		if source.Name != "kitedrive" {
			if user, found := fta_user_map[source.Creator]; found {
				if user == 2 {
					kw_user = source.Creator
				}
			}

			// Fallback to admin account.
			if kw_user == NONE {
				Notice("%s: Workspace owner does not exist or does not have rights to workspace, will creates as '%s' instead.", v, T.ppt.Username)
				kw_user = T.ppt.Username
			}

			// Find a reader account.
			for _, u := range ws_users {
				if u.UserType == 1 || u.UserType == 2 {
					fta_user = strings.ToLower(u.UserID)
					break
				}
			}

			if kw_user != T.ppt.Username {
				if kwu, found := kw_user_map[kw_user]; found {
					kw_user = kwu
				} else {
					user, err := T.KWUser(kw_user, true, false)
					if err != nil {
						Err("%s: %v", v, err)
						continue
					}
					profile, err := T.ppt.Profile(user.UserTypeID).Get()
					if err != nil {
						Err("%s(%s): %v", v, kw_user, err)
						continue
					}
					if profile.Features.FolderCreate == 0 {
						Notice("%s(%s): User's kiteworks profile is not allowed to create folders, will create as '%s' instead.", v, kw_user, T.ppt.Username)
						kw_user = T.ppt.Username
					}
					kw_user_map[user.Email] = T.ppt.Username
				}
			}
		} else {
			fta_user = T.user
			kw_user = T.user
		}

		dest, err := T.ppt.Session(kw_user).Folder(0).ResolvePath(strings.Replace(v, "kitedrive", "My Folder", 1))
		if err != nil {
			Err("%s: %v", v, err)
			continue
		}
		T.ProcessWorkspace(kw_user, fta_user, &dest, &source)
	}

	//T.upload_wg.Wait()
	return
}
