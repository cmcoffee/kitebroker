package admin

import (
	. "github.com/cmcoffee/kitebroker/core"
	"os"
	"strings"
	"encoding/csv"
	"sync"
	"fmt"
	"sort"
	"path/filepath"
	"time"
	"bufio"
)

var folder_perms = map[string]int{
	"downloader": 2,
	"collaborator": 3,
	"manager": 4, 
	"viewer": 2,
}

type CSVOnboardTask struct {
	// input variables
	input struct {
		csv_file           string
		manager            string
		out_path           string
		subscribe          bool
	}
	user_count   Tally
	folder_count Tally
	record_lock  sync.Mutex
	folder_map   map[string]string
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
	T.Flags.StringVar(&T.input.csv_file, "in_file", "users.csv", "CSV File to Import from.")
	T.Flags.StringVar(&T.input.out_path, "out_path", "<.>", "Folder to save completed CSVs to.")
	T.Flags.BoolVar(&T.input.subscribe, "sub", "Subscribe new members to notifications.")
	T.Flags.Order("in_file","out_path","manager")
	if err := T.Flags.Parse(); err != nil {
		return err
	}

	return
}

func (T *CSVOnboardTask) LookupFolder(path string) (string, error) {
	T.record_lock.Lock()
	defer T.record_lock.Unlock()
	path = strings.ToLower(path)
	if v, ok := T.folder_map[path]; !ok {
		obj, err := T.KW.Session(T.input.manager).Folder("0").ResolvePath(path)
		if err != nil {
			return obj.ID, err
		}
		T.folder_map[path] = obj.ID
		return obj.ID, nil
	} else {
		return v, nil
	}
} 

func (T CSVOnboardTask) LookupPermission(permission string) (int, error) {
	lc_permission := strings.ToLower(permission)
	if v, ok := folder_perms[lc_permission]; !ok {
		var perms_list []string 
		for k, _ := range folder_perms {
			perms_list = append(perms_list, k)
		}
		sort.Strings(perms_list)
		return 0, fmt.Errorf("'%s' not found in available permissions: %v.", permission, perms_list)
	} else {
		return v, nil
	}
}

func (T *CSVOnboardTask) AddUsersToFolder(path, permission string, users []string) (err error) {
	role_id, err := T.LookupPermission(permission)
	if err != nil {
		return err
	}
	folder_id, err := T.LookupFolder(path)
	if err != nil {
		return err
	}
	err = T.KW.Session(T.input.manager).Folder(folder_id).AddUsersToFolder(users, role_id, true, T.input.subscribe)
	if IsAPIError(err, "ERR_ENTITY_ROLE_IS_ASSIGNED") {
		err = nil
	}
	Log("[%s]: Added to '%s' as %s.", strings.Join(users, ";"), path, permission)
	return
}

func (T *CSVOnboardTask) Main() (err error) {
	if IsBlank(T.input.manager) {
		T.input.manager = T.KW.Username
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

	errors_filename := fmt.Sprintf("%s-%d.failures.csv", strings.TrimSuffix(filename,ext), current_time)
	done_filename := fmt.Sprintf("%s-%d.complete.csv", strings.TrimSuffix(filename,ext), current_time)

	f, err := os.Open(T.input.csv_file)
	if err != nil {
		if os.IsNotExist(err) {
			Log(err)
			return nil
		}
		return err
	}	
	scanner := bufio.NewScanner(f)

	err_file, err := os.OpenFile(NormalizePath(fmt.Sprintf("%s/%s", out_path, errors_filename)), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		f.Close()
		return err
	}

	done_file, err := os.OpenFile(NormalizePath(fmt.Sprintf("%s/%s", out_path, done_filename)), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
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
		users := strings.Split(row[2], ",")
		err = T.AddUsersToFolder(row[0], row[1], users)
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
	f.Close()
	if errors == 0 {
		Critical(os.Remove(NormalizePath(fmt.Sprintf("%s/%s", out_path, errors_filename))))
	}
	Critical(os.Remove(T.input.csv_file))
	return nil
}
