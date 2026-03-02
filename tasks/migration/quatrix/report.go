package quatrix

import (
	"sort"
	"strings"
	"sync"

	. "github.com/cmcoffee/kitebroker/core"
)

type reportEntry struct {
	Path string
	Name string
	Type string
	Size int64
}

type permEntry struct {
	Email      string
	Operations int64
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

// MapPermissionName maps a Quatrix operations bitmask to a human-readable role name.
func MapPermissionName(quatrix_perms int64) string {
	x := BitFlag(quatrix_perms)
	switch {
	case x.Has(QP_PREVIEW) && !x.Has(QP_DOWNLOAD):
		return "Viewer"
	case x.Has(QP_DOWNLOAD) && !x.Has(QP_UPLOAD):
		return "Downloader"
	case x.Has(QP_UPLOAD) && !x.Has(QP_DOWNLOAD):
		return "Uploader"
	case x.Has(QP_MANAGE):
		return "Manager"
	case x.Has(QP_UPLOAD) && x.Has(QP_DOWNLOAD):
		return "Collaborator"
	default:
		return "Unknown"
	}
}

// getUserReport returns the userReport for the given email, creating one if needed.
func (T *QuatrixMigrationTask) getUserReport(email string) *userReport {
	T.report_data.lock.Lock()
	defer T.report_data.lock.Unlock()
	if ur, ok := T.report_data.users[email]; ok {
		return ur
	}
	ur := &userReport{email: email}
	T.report_data.users[email] = ur
	return ur
}

// RunReport generates a report of Quatrix users, folders and files.
func (T *QuatrixMigrationTask) RunReport(users []Userdata) (err error) {
	T.UserCount = T.Report.Tally("Users")
	T.FolderCount = T.Report.Tally("Folders")
	T.FileCount = T.Report.Tally("Files")
	T.Transferred = T.Report.Tally("Total Size", HumanSize)

	T.report_data.users = make(map[string]*userReport)

	Log("Generating Quatrix Report...")

	wg := NewLimitGroup(10)
	for _, u := range users {
		wg.Add(1)
		go func(user Userdata) {
			defer wg.Done()
			T.ReportUser(&user)
		}(u)
	}
	wg.Wait()

	T.displayTreeView()

	return nil
}

// ReportUser processes a single user for the report.
func (T *QuatrixMigrationTask) ReportUser(user *Userdata) {
	T.UserCount.Add(1)
	username := strings.ToLower(user.Email)

	user_folders, err := T.qsess.File(user.HomeID)
	if err != nil {
		Err("[%s]: Error reading user folders: %v", username, err)
		return
	}
	for _, f := range user_folders.Content {
		if f.IsSystemFolder() {
			continue
		}
		f, err := T.qsess.File(f.ID)
		if err != nil {
			Err("[%s]: Error reading folder %s: %v", username, f.Name, err)
			continue
		}
		T.ReportFolder(username, &f)
	}
}

// ReportFolder crawls a folder and reports its contents.
func (T *QuatrixMigrationTask) ReportFolder(username string, folder *QObject) {
	p := func(sess *QSession, obj *QObject) error {
		if obj.IsSystemFolder() {
			return nil
		}
		T.RegisterFolder(obj)
		path := T.Path(obj)
		switch obj.Type {
		case "D", "S":
			T.FolderCount.Add(1)
			T.WriteReportLine(username, "Folder", path, "", 0)
		case "F":
			T.FileCount.Add(1)
			T.Transferred.Add64(obj.Size)
			dir := path
			if idx := strings.LastIndex(path, "/"); idx >= 0 {
				dir = path[:idx]
			}
			T.WriteReportLine(username, "File", dir, obj.Name, obj.Size)
		}

		// Collect data for tree and permissions views.
		ur := T.getUserReport(username)
		ur.lock.Lock()
		ur.entries = append(ur.entries, reportEntry{
			Path: path,
			Name: obj.Name,
			Type: obj.Type,
			Size: obj.Size,
		})
		ur.lock.Unlock()

		if obj.Type == "S" {
			if perms, err := obj.Permissions(); err == nil {
				var pe []permEntry
				for _, p := range perms.Users {
					if strings.ToLower(p.Email) == username {
						continue
					}
					pe = append(pe, permEntry{
						Email:      p.Email,
						Operations: p.Operations,
					})
				}
				if len(pe) > 0 {
					ur.lock.Lock()
					ur.perms = append(ur.perms, sharedFolderPerms{
						Path:  path,
						Perms: pe,
					})
					ur.lock.Unlock()
				}
			}
		}

		return nil
	}
	T.qsess.FolderCrawler(p, folder)
}

// WriteReportLine outputs a report line to screen.
func (T *QuatrixMigrationTask) WriteReportLine(user, obj_type, path, name string, size int64) {
	if obj_type == "File" {
		Log("[%s]: %s - %s/%s (%s)", user, obj_type, path, name, HumanSize(size))
	} else {
		Log("[%s]: %s - %s", user, obj_type, path)
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
					case "F":
						child.isFile = true
						child.size = e.Size
					case "S":
						child.isShared = true
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
				Log("%s%s[U] %s (%s)", childPrefix, permConnector, p.Email, MapPermissionName(p.Operations))
			}
		}
		printTree(child, childPrefix)
	}
}

// displayTreeView renders the folder/file tree for each user after data collection.
func (T *QuatrixMigrationTask) displayTreeView() {
	var emails []string
	for email := range T.report_data.users {
		emails = append(emails, email)
	}
	sort.Strings(emails)

	Log("\n=== Quatrix Content Overview ===\n")

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
