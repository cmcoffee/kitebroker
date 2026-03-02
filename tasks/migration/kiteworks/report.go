package kiteworks

import (
	"sort"
	"strings"
	"sync"

	. "github.com/cmcoffee/kitebroker/core"
)

type reportEntry struct {
	Path string
	Name string
	Type string // "d" for folder, "f" for file
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
func (T *KW_TO_KWTask) getUserReport(email string) *userReport {
	T.report_data.lock.Lock()
	defer T.report_data.lock.Unlock()
	if ur, ok := T.report_data.users[email]; ok {
		return ur
	}
	ur := &userReport{email: email}
	T.report_data.users[email] = ur
	return ur
}

// RunReport generates a pre-migration report of source Kiteworks users, folders and files.
func (T *KW_TO_KWTask) RunReport(users []KiteUser) (err error) {
	T.users_count = T.Report.Tally("Users")
	T.folders_count = T.Report.Tally("Folders")
	T.files_count = T.Report.Tally("Files")
	T.transfer_counter = T.Report.Tally("Total Size", HumanSize)

	T.report_data.users = make(map[string]*userReport)

	Log("Generating Source Kiteworks Report...")

	wg := NewLimitGroup(10)
	for _, u := range users {
		wg.Add(1)
		go func(user KiteUser) {
			defer wg.Done()
			if user.Suspended {
				Log("[%s]: User is suspended.", user.Email)
				return
			}
			if !user.Verified {
				Log("[%s]: User is not verified.", user.Email)
				return
			}
			T.ReportUser(&user)
		}(u)
	}
	wg.Wait()

	T.displayTreeView()

	return nil
}

// ReportUser processes a single user for the report.
func (T *KW_TO_KWTask) ReportUser(user *KiteUser) {
	T.users_count.Add(1)
	username := user.Email
	src_sess := T.SRC.Session(username)

	src_folder, err := src_sess.Folder(user.BaseDirID).Info()
	if err != nil {
		Err("[%s]: Error reading user folders: %v", username, err)
		return
	}

	subfolders, err := src_sess.Folder(src_folder.ID).Folders()
	if err != nil {
		Err("[%s]: Error reading subfolders: %v", username, err)
		return
	}

	for i := range subfolders {
		if subfolders[i].Type == "d" {
			T.ReportFolder(username, src_sess, &subfolders[i])
		}
	}
}

// ReportFolder recursively crawls a source folder and reports its contents.
func (T *KW_TO_KWTask) ReportFolder(username string, src_sess KWSession, folder *KiteObject) {
	if folder.CurrentUserRole.ID != 5 || folder.Path == "basedir" {
		return
	}

	T.folders_count.Add(1)
	Log("[%s]: Folder - %s", username, folder.Path)

	ur := T.getUserReport(username)
	isShared := false

	// Get members for permissions.
	members, err := src_sess.Folder(folder.ID).Members()
	if err == nil && len(members) > 0 {
		var pe []permEntry
		for _, m := range members {
			if strings.EqualFold(m.User.Email, username) {
				continue
			}
			if IsBlank(m.User.Email) {
				continue
			}
			pe = append(pe, permEntry{
				Email: m.User.Email,
				Role:  m.Role.Name,
			})
		}
		if len(pe) > 0 {
			isShared = true
			ur.lock.Lock()
			ur.perms = append(ur.perms, sharedFolderPerms{
				Path:  folder.Path,
				Perms: pe,
			})
			ur.lock.Unlock()
		}
	}

	entryType := "d"
	if isShared {
		entryType = "s"
	}

	ur.lock.Lock()
	ur.entries = append(ur.entries, reportEntry{
		Path: folder.Path,
		Name: folder.Name,
		Type: entryType,
	})
	ur.lock.Unlock()

	// Get files.
	files, err := src_sess.Folder(folder.ID).Files()
	if err == nil {
		for _, f := range files {
			if f.Type != "f" {
				continue
			}
			T.files_count.Add(1)
			T.transfer_counter.Add64(f.Size)

			ur.lock.Lock()
			ur.entries = append(ur.entries, reportEntry{
				Path: folder.Path + "/" + f.Name,
				Name: f.Name,
				Type: "f",
				Size: f.Size,
			})
			ur.lock.Unlock()

			Log("[%s]: File - %s/%s (%s)", username, folder.Path, f.Name, HumanSize(f.Size))
		}
	}

	// Get subfolders and recurse.
	subfolders, err := src_sess.Folder(folder.ID).Folders()
	if err == nil {
		for i := range subfolders {
			if subfolders[i].Type == "d" {
				T.ReportFolder(username, src_sess, &subfolders[i])
			}
		}
	} else {
		Err("[%s] - %s: %v", username, folder.Path, err)
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
					case "f":
						child.isFile = true
						child.size = e.Size
					case "s":
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
func (T *KW_TO_KWTask) displayTreeView() {
	var emails []string
	for email := range T.report_data.users {
		emails = append(emails, email)
	}
	sort.Strings(emails)

	Log("\n=== Source Kiteworks Content Overview ===\n")

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
