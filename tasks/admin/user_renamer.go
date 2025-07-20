package admin

import (
	"encoding/csv"
	"fmt"
	. "kitebroker/core"
	"os"
	"strings"
)

type UserRenamerTask struct {
	input struct {
		all_users bool
		csv_file  string
	}
	user_count   Tally
	user_renamed Tally
	records      [][]string
	KiteBrokerTask
}

func (T UserRenamerTask) New() Task {
	return new(UserRenamerTask)
}

func (T UserRenamerTask) Name() string {
	return "user_renamer"
}

func (T UserRenamerTask) Desc() string {
	return "Rename email accounts with CSV."
}

func (T *UserRenamerTask) Init() (err error) {
	T.Flags.StringVar(&T.input.csv_file, "csv_file", "<migrate_users.csv>", "Users Report CSV File")
	if err = T.Flags.Parse(); err != nil {
		return err
	}

	if IsBlank(T.input.csv_file) {
		return fmt.Errorf("--csv_file is required.")
	}

	return
}

func (T *UserRenamerTask) Main() (err error) {
	T.user_count = T.Report.Tally("Accounts evaluated")
	T.user_renamed = T.Report.Tally("Accounts renamed")

	limiter := NewLimitGroup(50)

	err = T.ReadCSV(T.input.csv_file)
	if err != nil {
		return err
	}

	user_list := len(T.records)

	message := func() string {
		return fmt.Sprintf("Please wait ... [users: processed %d of %d, renamed %d]", T.user_count.Value(), user_list, T.user_renamed.Value())
	}

	PleaseWait.Set(message, []string{"[>  ]", "[>> ]", "[>>>]", "[ >>]", "[  >]", "[  <]", "[ <<]", "[<<<]", "[<< ]", "[<  ]"})
	PleaseWait.Show()

	for _, record := range T.records {
		limiter.Add(1)
		go func(old, new string) {
			defer limiter.Done()
			err = T.RenameUser(old, new)
			if err != nil {
				Err("%s -> %s: %s", old, new, err.Error())
			}
		}(record[0], record[1])
	}
	limiter.Wait()

	return nil
}

func (T *UserRenamerTask) ReadCSV(file string) (err error) {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	reader := csv.NewReader(f)
	T.records, err = reader.ReadAll()
	if err != nil {
		return err
	}

	for i, val := range T.records {
		if i == 0 {
			continue
		}
		T.records[i][0] = strings.TrimSpace(strings.ToLower(val[0]))
		T.records[i][1] = strings.TrimSpace(strings.ToLower(val[1]))
	}
	return nil
}

func (T UserRenamerTask) RenameUser(old_email, new_email string) (err error) {
	err = T.KW.Admin().MigrateEmails([]map[string]string{{
		"oldEmail": old_email,
		"newEmail": new_email,
	}}, true)
	T.user_count.Add(1)
	if err != nil {
		return err
	}

	Log("%s -> %s: Success!", old_email, new_email)

	T.user_renamed.Add(1)
	return
}
