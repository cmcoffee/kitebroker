package main

import (
	"encoding/csv"
	"os"
	"sync"
	"strconv"
	"fmt"
	"strings"
	"github.com/cmcoffee/go-nfo"
)

func (j Session) CSVOnboard() (err error) {
	var f *os.File
	f, err = os.Open(Config.Get("csv_onboarding:opts", "csv_file"))
	if err != nil {
		return err
	}
	reader := csv.NewReader(f)

	data, err := reader.ReadAll()
	if err != nil {
		return err
	}


	if len(data[0]) < 5 {
		nfo.Fatal("%s: Invalid CSV format.", Config.Get("csv_onboarding:opts", "csv_file"))
		return
	}

	folder_user_type := func(role string) (id int) {
		role = strings.ToLower(role)
		switch role {
			case "downloader":
				return 2
			case "collaborator":
				return 3
			case "manager":	
				return 4
			case "owner":
				return 5
			case "viewer":
				return 6
			default:
				return -1
		}
	}

	notify := func() bool {
		nfo.Log("")
		defer nfo.Log("")
		n := strings.ToLower(Config.Get("csv_onboarding:opts", "send_notifications"))
		if n == "true" || n == "yes" {
			nfo.Notice("Notifications are enabled.")
			return true
		} else {
			nfo.Notice("Notifications are disabled.")
			return false
		}
	}()

	file_change_notify := func() bool {
		n := strings.ToLower(Config.Get("csv_onboarding:opts", "subscribe_file_notifications"))
		if n == "true" || n == "yes" {
			return true
		} else {
			return false
		}
	}()

	mgr_folder_role := folder_user_type(Config.Get("csv_onboarding:opts", "manager_folder_role"))
	if mgr_folder_role < 0 {
		nfo.Fatal("Please check \"manager_folder_role\", provided role \"%s\" does not exist!", Config.Get("csv_onboarding:opts", "manager_folder_role"))
		return
	}

	ext_folder_role := folder_user_type(Config.Get("csv_onboarding:opts", "ext_folder_role"))
	if ext_folder_role < 0 {
		nfo.Fatal("Please check \"ext_folder_role\", provided role \"%s\" does not exist!", Config.Get("csv_onboarding:opts", "ext_folder_role"))
		return
	}

	top := Config.Get("csv_onboarding:opts", "primary_folder")
	sub_folders := Config.MGet("csv_onboarding:opts", "sub_folders")

	var top_id int

	nfo.Log("Processing folder: %s\n", top)

	top_id, _ = j.FindFolder(top)
	if top_id < 0 {
		if base_id, err := j.MyBaseDirID(); err != nil {
			return err
		} else {
			top_id, err = j.CreateFolder(base_id, top)
			if err != nil {
				return err
			}
		}
	}

	var wg sync.WaitGroup

	thtl := make(chan struct{}, 15)
	for i := 0; i < 15; i++ {
		thtl<-struct{}{}
	}

	retry := 3

	for _, row := range data {
		<-thtl
		wg.Add(1)
		go func(row []string) {
			defer wg.Done()
			defer func() {thtl<-struct{}{}}()

			nfo.Log("Processing folder: %s/%s", top, row[0])

			mgr_role_id, err := strconv.Atoi(Config.Get("csv_onboarding:opts", "manager_newuser_profile_id"))
			if err != nil {
				nfo.Warn("%s: Defaulting to Standard.", err)
				mgr_role_id = 1
			}
			ext_role_id, err := strconv.Atoi(Config.Get("csv_onboarding:opts", "ext_newuser_profile_id"))
			if err != nil {
				nfo.Warn("%s: Defaulting to Restricted.", err)
				ext_role_id = 4
			}

			for i := 0; i < retry; i++ {
				if _, err := j.FindUser(row[3]); err != nil {
					if _, err = j.NewUser(row[3], mgr_role_id, false, notify); err != nil && !strings.Contains(err.Error(), "Entity exists") {
						nfo.Warn("%s: %s [Attempt %d/%d]", row[3], err, i + 1, retry)
					} else {
						if i > 0 {
							nfo.Notice("%s: Success!", row[3])
						}
						break
					}
				} else {
					if i > 0 {
						nfo.Notice("%s: Success!", row[4])
					}
					break
				}
			}

			for i := 0; i < retry; i++ {
				if _, err := j.FindUser(row[4]); err != nil {
					if _, err = j.NewUser(row[4], ext_role_id, false, notify); err != nil && !strings.Contains(err.Error(), "Entity exists") {
						nfo.Warn("%s: %s [Attempt %d/%d]", row[4], err, i + 1, retry)
					} else {
						if i > 0 {
							nfo.Notice("%s: Success!", row[4])
						}
						break
					}
				} else {
					if i > 0 {
						nfo.Notice("%s: Success!", row[4])
					}
					break
				}
			}

			var folder_id int

			for i := 0; i < retry; i++ {
				folder_id, _ = j.FindChildFolder(top_id, row[0])
				if folder_id < 0 {
					folder_id, err = j.CreateFolder(top_id, row[0])
					if err != nil {
						nfo.Warn("%s: %s [Attempt %d/%d]", row[0], err, i + 1, retry)
						continue
					} else {
						if i > 0 {
							nfo.Notice("%s: Success!", row[0])
						}
						break
					}
				} else {
					if i > 0 {
						nfo.Notice("%s: Success!", row[0])
					}
					break
				}
			}


			for i := 0; i < retry; i++ {
				if err := j.RemoveEmailFromFolder(row[3], folder_id, true); err != nil {
					continue
				}
				break
			}


			for i := 0; i < retry; i++ {
				if err := j.RemoveEmailFromFolder(row[4], folder_id, true); err != nil {
					continue
				}
				break
			}

			for i := 0; i < retry; i++ {
				if err := j.AddEmailToFolder(row[3], folder_id, mgr_folder_role, notify, file_change_notify); err != nil && !strings.Contains(err.Error(), "Cannot assign already assigned role") {
					nfo.Warn("%s -> %s: %s [Attempt %d/%d]", row[3], row[0], err, i + 1, retry)
				} else {
					if i > 0 {
						nfo.Notice("%s -> %s: Success!", row[3], row[0])
					}
					break
				}
			}

			for i := 0; i < retry; i++ {
				if err := j.AddEmailToFolder(row[4], folder_id, ext_folder_role, notify, file_change_notify); err != nil && !strings.Contains(err.Error(), "Cannot assign already assigned role") {
					nfo.Warn("%s -> %s: %s [Attempt %d/%d]", row[4], row[0], err, i + 1, retry)
				} else {
					if i > 0 {
						nfo.Notice("%s -> %s: Success!", row[4], row[0])
					}
					break
				}
			}

			for i := 0; i < retry; i++ {
				if err = j.ChangeFolder(folder_id, fmt.Sprintf("{\"description\":\"%s\", \"fileLifetime\": %s, \"applyFileLifetimeToFiles\":true, \"applyFileLifetimeToNested\":true}", row[1], row[2])); err != nil {
						nfo.Warn("%s: %s [Attempt %d/%d]", row[0], err, i + 1, retry)
				} else {
					if i > 0 {
						nfo.Notice("%s: Success!", row[0])
					}
					break
				}
			}

			for _, sub := range sub_folders {
				var sub_folder_id int

				path := fmt.Sprintf("%s/%s/%s", top, row[0], sub)
				nfo.Log("Processing folder: %s", path)
				for i := 0; i < retry; i++ {
					if sub_folder_id, _ = j.FindChildFolder(folder_id, sub); sub_folder_id < 0 {
						if sub_folder_id, err = j.CreateFolder(folder_id, sub); err != nil {
							nfo.Warn("%s: %s [Attempt %d/%d]", path, err, i + 1, retry)
						} else {
							if i > 0 {
								nfo.Notice("%s: Success!", path)
							}
							break
						}
					} else {
						if i > 0 {
							nfo.Notice("%s: Success!", path)
						}
						break
					}
				}

			}
		}(row)
	}
	wg.Wait()

	return

}