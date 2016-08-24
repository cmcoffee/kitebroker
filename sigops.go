package main

import (
	"os"
	"encoding/csv"
	"strings"
	"fmt"
	"io"
)

func extendedOperations(args []string) {
	ext = NewCache()
}

// Add User via CSV
func Add_Accounts_CSV(admin_account string, verify, notify bool) (err error) {
	csv_file := Config.SGet("create_accounts", "csv_file")
	f, err := os.Open(csv_file)
	if err != nil {
		return err
	}
	r := csv.NewReader(f)

	c := NewCache()
	defer c.Flush()

	s := Session(admin_account)

	_, err = s.MyUser()
	if err != nil {
		return fmt.Errorf("admin_bind: %s", err)
	}

	roles, err := s.GetRoles()
	if err != nil {
		return fmt.Errorf("admin_bind: %s", err)
	}

	for _, elem := range roles.Data {
		c.Set("role_map", elem.Name, elem.ID)
	}

	// Performs calls to add user, adding the folder id to the map and account_id as well.
	add_user := func(account, folder, role string) (err error) {
		folder_id, found := c.GetID("folder_map", folder)

		if !found {
			folder_id, err = s.FindFolder(folder)

			if err != nil {
				folder_id = -1
			}

			c.Set("folder_map", folder, folder_id)
		}

		// Create folders if they do not exist.
		if folder_id < 0 {
			split_folders := strings.Split(folder, "/")
			split_len := len(split_folders)
			for i := split_len; i > split_len; i-- {
				folder_id, _ = s.FindFolder(strings.Join(split_folders[0:i], "/"))
				if folder_id > 0 {
					for n := i; n < split_len; n++ {
						folder_id, err = s.CreateFolder(split_folders[n], folder_id)
						if err != nil {
							return err
						}
					}
				}
			}
		}

		role_id, found := c.GetID("role_map", role)

		if !found {
			return fmt.Errorf("Role %s not found when trying to add %s to %s.", role, account, folder)
		}

		account_id, _ := c.GetID("account_map", account)

		return s.AddUserToFolder(account_id, folder_id, role_id, false)
	}

	for {
		records, err := r.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		if len(records) < 1 {
			continue
		}

		wg.Add(1)
		go func(records []string) {
			defer wg.Done()

			var account_id int

			err = s.AddUser(records[0], records[1], verify, notify)
			if err != nil {
				if !strings.Contains(err.Error(), "409") {
					fmt.Printf("\r- Error: %s\n", err.Error())
					goto Done
				} else {
					fmt.Printf("\rAccount %s already exists.\n", records[0])
				}
			} else {
				fmt.Printf("\rAccount %s added to system.\n", records[0])
			}
			account_id, err = s.FindUser(records[0])
			if err != nil {
				fmt.Printf("\r- Error: %s\n", err.Error())
				goto Done
			}

			c.Set("account_map", records[0], account_id)

			for i := 2; i < len(records)-2; i = i + 2 {
				err = add_user(records[0], records[i], records[i+1])
				if err != nil {
					if !strings.Contains(err.Error(), "409") {
						fmt.Printf("\r- Error: Account[%s] Folder[%s] Role[%s]: %s\n", records[0], records[i], records[i+1], err.Error())
					} else {
						fmt.Printf("\r%s is already member of folder %s.\n", records[0], records[i])
					}
				} else {
					fmt.Printf("\rAdded %s to folder %s.\n", records[0], records[i])
				}
			}
		Done:
		}(records)
	}

	wg.Wait()
	return nil
}
