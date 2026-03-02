package box

import (
	"sort"
	"strings"
	"sync"

	. "github.com/cmcoffee/kitebroker/core"
)

type reportEntry struct {
	Path string
	Name string
	Type string // "folder" or "file"
	Size int64
}

type permEntry struct {
	Email string
	Role  string
}

type sharedFolderPerms struct {
	Path  string
	Perms []permEntry
}

type userReport struct {
	email   string
	entries []reportEntry
	perms   []sharedFolderPerms
	lock    sync.Mutex
}

type treeNode struct {
	name        string
	size        int64
	isFile      bool
	isShared    bool
	fileCount   int
	folderCount int
	userCount   int
	perms       []permEntry
	children    []*treeNode
}

// getUserReport returns the userReport for the given email, creating one if needed.
func (T *BoxMigrationTask) getUserReport(email string) *userReport {
	T.report_data.lock.Lock()
	defer T.report_data.lock.Unlock()
	if ur, ok := T.report_data.users[email]; ok {
		return ur
	}
	ur := &userReport{email: email}
	T.report_data.users[email] = ur
	return ur
}

// RunReport generates a pre-migration report of Box.com users, folders and files.
func (T *BoxMigrationTask) RunReport(users []BoxUserRecord) (err error) {
	T.UserCount = T.Report.Tally("Users")
	T.FolderCount = T.Report.Tally("Folders")
	T.FileCount = T.Report.Tally("Files")
	T.Transferred = T.Report.Tally("Total Size", HumanSize)
	T.CommentCount = T.Report.Tally("Comments")
	T.TaskCount = T.Report.Tally("Tasks")

	T.report_data.users = make(map[string]*userReport)

	Log("Generating Box.com Report...")

	wg := NewLimitGroup(10)
	for _, u := range users {
		wg.Add(1)
		go func(user BoxUserRecord) {
			defer wg.Done()
			T.ReportUser(&user)
		}(u)
	}
	wg.Wait()

	T.displayTreeView()

	return nil
}

// ReportUser processes a single user for the report.
func (T *BoxMigrationTask) ReportUser(user *BoxUserRecord) {
	T.UserCount.Add(1)
	username := strings.ToLower(user.Login)
	if username == NONE || username == "unknown_box_user@box.com" {
		return
	}

	sess := T.bapi.Session(user.ID)
	baseFolder, err := sess.Folder("0")
	if err != nil {
		Err("[%s]: Error reading user folders: %v", username, err)
		return
	}

	// Collect owned top-level folders for the crawler.
	var ownedFolders []*BoxFolder
	for _, f := range baseFolder.Items {
		if f.Type != "folder" {
			continue
		}
		folder, err := sess.Folder(f.ID)
		if err != nil {
			Err("[%s]: Error reading folder %s: %v", username, f.Name, err)
			continue
		}
		if folder.Owner != username {
			continue
		}
		ownedFolders = append(ownedFolders, folder)
	}

	// Define the processor callback for the FolderCrawler.
	processor := func(bsess *BoxSession, folder *BoxFolder, item *BoxFolderItem) error {
		if item == nil {
			// Folder processing.
			T.FolderCount.Add(1)
			Log("[%s]: Folder - %s", username, folder.FullPath)

			ur := T.getUserReport(username)
			ur.lock.Lock()
			ur.entries = append(ur.entries, reportEntry{
				Path: folder.FullPath,
				Name: folder.Name,
				Type: "folder",
			})
			ur.lock.Unlock()

			// Collect permissions for shared folders.
			if len(folder.Permissions) > 0 {
				var pe []permEntry
				for _, p := range folder.Permissions {
					if p.User == username {
						continue
					}
					pe = append(pe, permEntry{
						Email: p.User,
						Role:  permRoleName(p.Role),
					})
				}
				if len(pe) > 0 {
					ur.lock.Lock()
					ur.perms = append(ur.perms, sharedFolderPerms{
						Path:  folder.FullPath,
						Perms: pe,
					})
					ur.lock.Unlock()
				}
			}
			return nil
		}

		// File processing.
		versions, err := bsess.FileVersions(item.ID)
		if err != nil {
			return err
		}
		if len(versions) > 0 {
			latest := versions[len(versions)-1]
			T.FileCount.Add(1)
			T.Transferred.Add64(latest.Size)

			ur := T.getUserReport(username)
			ur.lock.Lock()
			ur.entries = append(ur.entries, reportEntry{
				Path: folder.FullPath + "/" + latest.Name,
				Name: latest.Name,
				Type: "file",
				Size: latest.Size,
			})
			ur.lock.Unlock()

			Log("[%s]: File - %s/%s (%s)", username, folder.FullPath, latest.Name, HumanSize(latest.Size))
		}

		// Count comments and tasks.
		if comments, err := bsess.FileComments(item.ID); err == nil {
			T.CommentCount.Add(len(comments))
		}
		if tasks, err := bsess.FileTasks(item.ID); err == nil {
			T.TaskCount.Add(len(tasks))
		}
		return nil
	}

	if len(ownedFolders) > 0 {
		sess.FolderCrawler(processor, ownedFolders...)
	}

	// Report root-level files (files directly in folder "0").
	for _, f := range baseFolder.Items {
		if f.Type != "file" {
			continue
		}
		versions, err := sess.FileVersions(f.ID)
		if err != nil {
			continue
		}
		if len(versions) > 0 {
			latest := versions[len(versions)-1]
			T.FileCount.Add(1)
			T.Transferred.Add64(latest.Size)

			ur := T.getUserReport(username)
			ur.lock.Lock()
			ur.entries = append(ur.entries, reportEntry{
				Path: username + "/" + latest.Name,
				Name: latest.Name,
				Type: "file",
				Size: latest.Size,
			})
			ur.lock.Unlock()

			Log("[%s]: File - %s/%s (%s)", username, username, latest.Name, HumanSize(latest.Size))
		}
	}
}

// permRoleName maps a Kiteworks role ID back to a human-readable name.
func permRoleName(roleID int) string {
	switch roleID {
	case 2:
		return "Downloader"
	case 3:
		return "Collaborator"
	case 4:
		return "Manager"
	case 6:
		return "Viewer"
	case 7:
		return "Uploader"
	default:
		return "Unknown"
	}
}

// buildTree constructs a treeNode hierarchy from a flat list of report entries
// and attaches permissions to matching shared folder nodes.
func buildTree(entries []reportEntry, folderPerms []sharedFolderPerms) *treeNode {
	root := &treeNode{}
	for _, e := range entries {
		path := strings.TrimPrefix(e.Path, "/")
		parts := strings.Split(path, "/")
		current := root
		for i, part := range parts {
			isLast := (i == len(parts)-1)
			var child *treeNode
			for _, c := range current.children {
				if c.name == part {
					child = c
					break
				}
			}
			if child == nil {
				child = &treeNode{name: part}
				if isLast {
					switch e.Type {
					case "file":
						child.isFile = true
						child.size = e.Size
					}
				}
				current.children = append(current.children, child)
			}
			current = child
		}
	}

	// Build a path-to-node lookup for attaching permissions.
	permMap := make(map[string]*treeNode)
	var walkPaths func(node *treeNode, path string)
	walkPaths = func(node *treeNode, path string) {
		for _, c := range node.children {
			cp := c.name
			if path != "" {
				cp = path + "/" + c.name
			}
			permMap[cp] = c
			walkPaths(c, cp)
		}
	}
	walkPaths(root, "")

	for _, sp := range folderPerms {
		key := strings.TrimPrefix(sp.Path, "/")
		if n, ok := permMap[key]; ok {
			n.isShared = true
			sorted := make([]permEntry, len(sp.Perms))
			copy(sorted, sp.Perms)
			sort.Slice(sorted, func(i, j int) bool {
				return sorted[i].Email < sorted[j].Email
			})
			n.perms = sorted
		}
	}

	sortTree(root)
	return root
}

// sortTree recursively sorts tree children: folders first, then alphabetical.
func sortTree(node *treeNode) {
	sort.Slice(node.children, func(i, j int) bool {
		if !node.children[i].isFile && node.children[j].isFile {
			return true
		}
		if node.children[i].isFile && !node.children[j].isFile {
			return false
		}
		return node.children[i].name < node.children[j].name
	})
	for _, c := range node.children {
		sortTree(c)
	}
}

// tallySize recursively sums file sizes, file counts, folder counts and user counts for each folder node.
func tallySize(node *treeNode) (int64, int, int, int) {
	if node.isFile {
		return node.size, 1, 0, 0
	}
	var totalSize int64
	var files, folders, users int
	users += len(node.perms)
	for _, c := range node.children {
		s, f, d, u := tallySize(c)
		totalSize += s
		files += f
		folders += d
		users += u
		if !c.isFile {
			folders++
		}
	}
	node.size = totalSize
	node.fileCount = files
	node.folderCount = folders
	node.userCount = users
	return totalSize, files, folders, users
}

// printTree recursively prints the tree using box-drawing characters.
func printTree(node *treeNode, prefix string) {
	for i, child := range node.children {
		isLast := (i == len(node.children)-1)
		connector := "├── "
		if isLast {
			connector = "└── "
		}
		childPrefix := prefix + "│   "
		if isLast {
			childPrefix = prefix + "    "
		}
		if child.isFile {
			Log("%s%s[F] %s (%s)", prefix, connector, child.name, HumanSize(child.size))
		} else {
			tag := "[D]"
			if child.isShared {
				tag = "[S]"
			}
			Log("%s%s%s %s (%d folders, %d files, %d users, %s)", prefix, connector, tag, child.name, child.folderCount, child.fileCount, child.userCount, HumanSize(child.size))
			// Print permissions as [U] entries within the tree.
			for j, p := range child.perms {
				permIsLast := (j == len(child.perms)-1) && len(child.children) == 0
				permConnector := "├── "
				if permIsLast {
					permConnector = "└── "
				}
				Log("%s%s[U] %s (%s)", childPrefix, permConnector, p.Email, p.Role)
			}
		}
		printTree(child, childPrefix)
	}
}

// displayTreeView renders the folder/file tree for each user after data collection.
func (T *BoxMigrationTask) displayTreeView() {
	var emails []string
	for email := range T.report_data.users {
		emails = append(emails, email)
	}
	sort.Strings(emails)

	Log("\n=== Box.com Content Overview ===\n")

	for _, email := range emails {
		ur := T.report_data.users[email]
		if len(ur.entries) == 0 {
			continue
		}
		root := buildTree(ur.entries, ur.perms)
		tallySize(root)
		Log("[%s]:", email)
		printTree(root, "")
		Log("")
	}
}
