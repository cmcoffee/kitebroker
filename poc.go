package main

import (
	"fmt"
	"os"
)

// Provides human readable file sizes.
func showSize(bytes int) string {

	names := []string{
		"Bytes",
		"KB",
		"MB",
		"GB",
	}

	suffix := 0
	size := float64(bytes)

	for size >= 1000 && suffix < len(names)-1 {
		size = size / 1000
		suffix++
	}

	return fmt.Sprintf("%.2f%s", size, names[suffix])
}

// This function is ugly, but does exactly for the POC what it was designed for. :)
func (s *Session) DownloadFolder(folder_id int, path string, folder_map map[int]string) error {

	first_run := false

	folder_list, err := s.ListFolders(folder_id)
	if err != nil {
		return err
	}
	file_list, err := s.ListFiles(folder_id)
	if err != nil {
		return err
	}

	// Initialize our folder map so we put the files in the right places.
	if folder_map == nil {
		first_run = true
		folder_map = make(map[int]string)
		folder_map[folder_id] = path
		finfo, err := os.Stat(path)
		if err != nil && os.IsNotExist(err) {
			err = os.Mkdir(path, 0755)
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		}

		// If the folder doesn't exist, create it.
		if finfo != nil && finfo.IsDir() == false {
			err = os.Remove(path)
			if err != nil {
				return err
			}
			err = os.Mkdir(path, 0755)
			if err != nil {
				return err
			}
		}
	}

	// Download files, push to background and continue, limited by max_threads.
	for _, file := range file_list.Data {
		if file.Deleted == true {
			continue
		}
		wg.Add()
		go func(file KiteData) {
			destination := cleanPath(folder_map[file.ParentID])
			fmt.Printf("- Downloading %s(%s) to %s...\n", file.Name, showSize(file.Size), destination)
			err = s.Download(file.ID, destination)
			if err != nil {
				fmt.Printf("- Error: %s\n", err.Error())
				fmt.Printf("- Error downloading %s to %s... [skipping, retry on next run.]\n", file.Name, destination)
			} else {
				s.MetaData(&file, destination)
				fmt.Printf("- Removing %s from %s.\n", file.Name, s.server)
				err = s.DeleteFile(file.ID)
				if err != nil {
					fmt.Printf("- Error: %s\n", err.Error())
				}
				err = s.KillFile(file.ID)
				if err != nil {
					fmt.Printf(" - Error: %s\n", err.Error())
				}
			}
			wg.Done()
		}(file)
	}

	// Recrusively visit nested folders.
	for _, folder := range folder_list.Data {
		if folder.Deleted == true {
			continue
		}
		f_path := cleanPath(fmt.Sprintf("%s/%s", path, folder.Name))
		folder_map[folder.ID] = f_path
		finfo, err := os.Stat(f_path)
		if err != nil && os.IsNotExist(err) {
			err = os.Mkdir(f_path, 0755)
			if err != nil {
				fmt.Printf("- Error: %s\n", err.Error())
			}
		} else if err != nil {
			fmt.Printf("- Error: %s\n", err.Error())
		}

		// If it's not a folder... we'll make it one.
		if finfo != nil && finfo.IsDir() == false {
			err = os.Remove(f_path)
			if err != nil {
				fmt.Printf("- Error: %s\n", err.Error())
			}
			err = os.Mkdir(f_path, 0755)
			if err != nil {
				fmt.Printf("- Error: %s\n", err.Error())
			}
		}
		err = s.DownloadFolder(folder.ID, f_path, folder_map)
		if err != nil {
			fmt.Printf("- Error: %s\n", err.Error())
		}
	}
	if first_run {
		wg.Wait()
	}
	return nil
}
