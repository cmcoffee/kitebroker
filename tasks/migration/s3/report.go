package s3

import (
	"fmt"
	"sort"

	. "github.com/cmcoffee/kitebroker/core"
)

// reportEntry captures a single object discovered during a dry-run report.
type reportEntry struct {
	Path string // Object key, e.g. "reports/2026/q1.pdf".
	Size int64
}

// treeNode is a node in the folder/file tree rendered by the report.
type treeNode struct {
	name        string
	size        int64
	isFile      bool
	fileCount   int
	folderCount int
	children    []*treeNode
}

// RunReport lists the objects that would be imported without uploading
// anything, then renders them as a folder/file tree under the destination
// folder. Mirrors the Box migration's --report behaviour.
func (T *S3MigrationTask) RunReport() (err error) {
	T.src, err = NewS3Source(T.cfg, T.KW.ProxyURI)
	if err != nil {
		return err
	}

	T.FileCount = T.Report.Tally("Files")
	T.FolderCount = T.Report.Tally("Folders")
	T.Transferred = T.Report.Tally("Total Size", HumanSize)

	Log("Generating S3 report for bucket '%s' (prefix: '%s')...", T.cfg.Bucket, T.cfg.Prefix)

	var entries []reportEntry
	err = T.src.ListObjects(T.cfg.Prefix, func(obj S3Object) error {
		T.FileCount.Add(1)
		T.Transferred.Add64(obj.Size)
		// Compute the true destination path via the same logic the import uses
		// so the preview matches the real run exactly.
		folder_path, name := destFolderPath(T.input.dst, obj.Key)
		full := name
		if !IsBlank(folder_path) {
			full = fmt.Sprintf("%s/%s", folder_path, name)
		}
		entries = append(entries, reportEntry{Path: full, Size: obj.Size})
		Log("File - %s (%s)", full, HumanSize(obj.Size))
		return nil
	})
	if err != nil {
		return err
	}

	T.displayTreeView(entries)
	return nil
}

// buildTree constructs a folder/file hierarchy from a flat list of full
// destination paths (each entry's Path is already the resolved Kiteworks path).
func buildTree(entries []reportEntry) *treeNode {
	root := &treeNode{}
	for _, e := range entries {
		parts := SplitPath(e.Path)
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
					child.isFile = true
					child.size = e.Size
				}
				current.children = append(current.children, child)
			}
			current = child
		}
	}
	sortTree(root)
	return root
}

// sortTree recursively orders children: folders first, then alphabetical.
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

// tallySize recursively sums sizes and counts for each folder node.
func tallySize(node *treeNode) (int64, int, int) {
	if node.isFile {
		return node.size, 1, 0
	}
	var totalSize int64
	var files, folders int
	for _, c := range node.children {
		s, f, d := tallySize(c)
		totalSize += s
		files += f
		folders += d
		if !c.isFile {
			folders++
		}
	}
	node.size = totalSize
	node.fileCount = files
	node.folderCount = folders
	return totalSize, files, folders
}

// printTree recursively prints the tree using box-drawing characters.
func printTree(node *treeNode, prefix string) {
	for i, child := range node.children {
		isLast := (i == len(node.children)-1)
		connector := "├── "
		childPrefix := prefix + "│   "
		if isLast {
			connector = "└── "
			childPrefix = prefix + "    "
		}
		if child.isFile {
			Log("%s%s[F] %s (%s)", prefix, connector, child.name, HumanSize(child.size))
		} else {
			Log("%s%s[D] %s (%d folders, %d files, %s)", prefix, connector, child.name, child.folderCount, child.fileCount, HumanSize(child.size))
		}
		printTree(child, childPrefix)
	}
}

// displayTreeView renders the destination folder tree after data collection.
// With a wrapper folder set, the tree is rooted at that folder. Without one,
// the top-level S3 prefixes are rendered as the top-level Kiteworks folders.
func (T *S3MigrationTask) displayTreeView(entries []reportEntry) {
	if len(entries) == 0 {
		Log("\n=== No objects found. ===")
		return
	}

	root := buildTree(entries)
	total, files, folders := tallySize(root)
	T.FolderCount.Add(folders)

	Log("\n=== S3 Import Preview (Kiteworks root, owner: %s) ===\n", T.input.owner)
	Log(fmt.Sprintf("Kiteworks:/ (%d folders, %d files, %s)", folders, files, HumanSize(total)))
	printTree(root, "")
	Log("")
}
