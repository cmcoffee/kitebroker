package admin

import (
	"bufio"
	"encoding/csv"
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var folder_perms = map[string]int{
	"downloader":   2,
	"collaborator": 3,
	"manager":      4,
	"viewer":       6,
	"uploader":     7,
}

type CSVOnboardTask struct {
	// input variables
	input struct {
		csv_file              string
		manager               string
		out_path              string
		subscribe             bool
		restricted_profile_id int
		internal_domains      []string
		downgrade_external    bool
		notify                bool
	}
	users_added  map[string]interface{}
	user_count   Tally
	folder_count Tally
	record_lock  sync.Mutex
	user_lock    sync.Mutex
	folder_map   map[string]string
	domain_map   map[string]interface{}
	// Required for all tasks
	KiteBrokerTask
}

func (T CSVOnboardTask) New() Task {
	return new(CSVOnboardTask)
}

func (T CSVOnboardTask) Name() string {
	return "csv_onboard"
}

func (T CSVOnboardTask) Desc() string {
	return "Add users to Folder."
}

func (T *CSVOnboardTask) Init() (err error) {
	T.Flags.StringVar(&T.input.manager, "manager", "<manager@domain.com>", "Manager of folders to add user with.\n\t(Kitebroker user will be used if none specified.)")
	T.Flags.StringVar(&T.input.csv_file, "in_file", "<users.csv>", "CSV File to Import from.")
	T.Flags.StringVar(&T.input.out_path, "out_path", "<.>", "Folder to save completed CSVs to.")
	T.Flags.BoolVar(&T.input.subscribe, "sub", "Subscribe new members to notifications.")
	T.Flags.MultiVar(&T.input.internal_domains, "internal_domain", "<domain.com>", "Internal Domains")
	T.Flags.IntVar(&T.input.restricted_profile_id, "restrict_profile_id", 0, "Profile ID for restricted users.")
	T.Flags.BoolVar(&T.input.downgrade_external, "downgrade_external", "Downgrade external members to downloader.")
	T.Flags.BoolVar(&T.input.notify, "notify", "Notify users of being added to folder.")
	T.Flags.Order("in_file", "out_path", "manager")
	T.Flags.InlineArgs("in_file")
	if err := T.Flags.Parse(); err != nil {
		return err
	}

	if IsBlank(T.input.csv_file) {
		return fmt.Errorf("must provide a csv file to process.")
	}

	return
}

func (T *CSVOnboardTask) IsInternal(user string) bool {
	lc_user := strings.ToLower(user)
	split := strings.Split(lc_user, "@")
	if len(split) < 2 {
		return false
	}
	if _, ok := T.domain_map[strings.ToLower(split[1])]; ok {
		return true
	}
	return false
}

func (T *CSVOnboardTask) CreateRestrictedUser(user string) (err error) {
	T.user_lock.Lock()
	defer T.user_lock.Unlock()

	lc_user := strings.ToLower(user)
	if _, ok := T.users_added[lc_user]; ok {
		return
	}

	if _, err := T.KW.Admin().NewUser(user, T.input.restricted_profile_id, true, false); err != nil {
		if !IsAPIError(err, "ERR_ENTITY_EXISTS") {
			T.users_added[lc_user] = struct{}{}
			return nil
		}
		return err
	}
	T.users_added[lc_user] = struct{}{}
	return nil
}

func (T *CSVOnboardTask) LookupFolder(path string) (string, error) {
	T.record_lock.Lock()
	defer T.record_lock.Unlock()
	l_path := strings.ToLower(path)
	if v, ok := T.folder_map[l_path]; !ok {
		obj, err := T.KW.Session(T.input.manager).Folder("0").ResolvePath(path)
		if err != nil {
			return obj.ID, err
		}
		T.folder_map[l_path] = obj.ID
		return obj.ID, nil
	} else {
		return v, nil
	}
}

func (T CSVOnboardTask) LookupPermission(permission string) (int, error) {
	lc_permission := strings.ToLower(permission)
	if v, ok := folder_perms[lc_permission]; !ok {
		var perms_list []string
		for k := range folder_perms {
			perms_list = append(perms_list, k)
		}
		sort.Strings(perms_list)
		return 0, fmt.Errorf("'%s' not found in available permissions: %v.", permission, perms_list)
	} else {
		return v, nil
	}
}

func (T *CSVOnboardTask) AddUsersToFolder(path, permission string, users []string, subscribe bool) (err error) {

	path = NormalizePath(path)

	add_users_to_folders := func(path, permission string, users []string) (err error) {
		role_id, err := T.LookupPermission(permission)
		if err != nil {
			return err
		}
		folder_id, err := T.LookupFolder(path)
		if err != nil {
			return err
		}
		err = T.KW.Session(T.input.manager).Folder(folder_id).AddUsersToFolder(users, role_id, T.input.notify, subscribe)
		if IsAPIError(err, "ERR_ENTITY_ROLE_IS_ASSIGNED") {
			err = nil
		}
		Log("[%s]: Added to '%s' as %s. (subscribe: %v)", strings.Join(users, ";"), path, permission, subscribe)
		return
	}

	if len(T.input.internal_domains) > 0 {
		var (
			int_users []string
			ext_users []string
		)

		for _, user := range users {
			if T.IsInternal(user) {
				int_users = append(int_users, user)
			} else {
				if T.input.restricted_profile_id > 0 {
					if e := T.CreateRestrictedUser(user); e != nil {
						Err(e.Error())
						continue
					}
				}
				ext_users = append(ext_users, user)
			}
		}
		if T.input.downgrade_external {
			if len(int_users) > 0 {
				if err := add_users_to_folders(path, permission, int_users); err != nil {
					return err
				}
			}

			permission = "downloader"
			if len(ext_users) < 1 {
				return nil
			}
			users = ext_users
		}
	}
	return add_users_to_folders(path, permission, users)
}

func (T *CSVOnboardTask) Main() (err error) {
	T.domain_map = make(map[string]interface{})
	T.users_added = make(map[string]interface{})
	if IsBlank(T.input.manager) {
		T.input.manager = T.KW.Username
	}

	for _, v := range T.input.internal_domains {
		T.domain_map[strings.ToLower(v)] = struct{}{}
	}

	out_path, filename := filepath.Split(T.input.csv_file)
	if IsBlank(out_path) {
		out_path = "."
	}

	if !IsBlank(T.input.out_path) {
		out_path = T.input.out_path
	}

	current_time := time.Now().Unix()

	ext := filepath.Ext(filename)

	errors_filename := fmt.Sprintf("%s-%d.failures.csv", strings.TrimSuffix(filename, ext), current_time)
	done_filename := fmt.Sprintf("%s-%d.complete.csv", strings.TrimSuffix(filename, ext), current_time)

	f, err := os.Open(T.input.csv_file)
	if err != nil {
		if os.IsNotExist(err) {
			Log(err)
			return nil
		}
		return err
	}
	scanner := bufio.NewScanner(f)

	err_file, err := os.OpenFile(LocalPath(fmt.Sprintf("%s/%s", out_path, errors_filename)), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		f.Close()
		return err
	}

	done_file, err := os.OpenFile(LocalPath(fmt.Sprintf("%s/%s", out_path, done_filename)), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		err_file.Close()
		f.Close()
		return err
	}

	T.folder_map = make(map[string]string)
	i := 0

	swap := new(SwapReader)
	csv_reader := csv.NewReader(swap)

	var errors int

	for scanner.Scan() {
		i++
		text := scanner.Text()
		if strings.HasPrefix(text, "#") {
			continue
		}
		swap.SetBytes([]byte(text))
		row, err := csv_reader.Read()
		if err != nil {
			Err(err)
			_, err = err_file.WriteString(fmt.Sprintf("# [ERROR] %v\n", err))
			Critical(err)
			_, err = err_file.WriteString(fmt.Sprintf("%s\n", text))
			Critical(err)
			errors++
			continue
		}
		row_len := len(row)

		if row_len < 3 {
			return fmt.Errorf("Invalid CSV provided, must provide: path,permission,email.")
		}

		if IsBlank(row[0]) || IsBlank(row[1]) || IsBlank(row[2]) {
			Err("[%s] Skipping line %d due to missing data: %s", T.input.csv_file, i, text)
			_, err = err_file.WriteString(fmt.Sprintf("# Entry skipped, not enough data."))
			Critical(err)
			_, err = err_file.WriteString(fmt.Sprintf("%s\n", text))
			Critical(err)
			errors++
			continue
		}

		subscribe := T.input.subscribe
		if row_len > 3 {
			if strings.ToLower(row[3]) == "yes" || strings.ToLower(row[3]) == "true" {
				subscribe = true
			}
		}
		users := strings.Split(row[2], ",")
		err = T.AddUsersToFolder(row[0], row[1], users, subscribe)
		if err != nil {
			Err(fmt.Errorf("[%s] record on line %d: %v", T.input.csv_file, i, err))
			_, err = err_file.WriteString(fmt.Sprintf("# [ERROR] %v\n", err))
			Critical(err)
			_, err = err_file.WriteString(fmt.Sprintf("%s\n", text))
			Critical(err)
			errors++
		} else {
			_, err = done_file.WriteString(fmt.Sprintf("%s\n", text))
			Critical(err)
		}
	}
	err_file.Close()
	done_file.Close()
	if errors == 0 {
		if err := os.Remove(LocalPath(fmt.Sprintf("%s/%s", out_path, errors_filename))); err != nil {
			Err(err)
		}
	}
	f.Close()
	if err := os.Remove(T.input.csv_file); err != nil {
		Err(err)
	}
	return nil
}
