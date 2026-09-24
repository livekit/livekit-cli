// Copyright 2026 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package skills

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// ContentHash is the `skills` CLI's project lock hash (computedHash): SHA-256
// over each file's relative path followed by its contents, in the order
// JavaScript's String.prototype.localeCompare sorts the paths. Matching it
// exactly lets lk recognize skills that `npx skills` installed, and vice versa.
func ContentHash(files []File) string {
	sorted := slices.Clone(files)
	c := collate.New(language.Und)
	sort.SliceStable(sorted, func(i, j int) bool {
		return c.CompareString(sorted[i].Path, sorted[j].Path) < 0
	})
	h := sha256.New()
	for _, f := range sorted {
		h.Write([]byte(f.Path))
		h.Write(f.Data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TreeHash is the git tree SHA-1 of a skill directory, which the `skills`
// CLI's global lock records as skillFolderHash.
func TreeHash(files []File) string {
	root := &treeNode{children: map[string]*treeNode{}}
	for _, f := range files {
		n := root
		parts := strings.Split(f.Path, "/")
		for _, p := range parts[:len(parts)-1] {
			child := n.children[p]
			if child == nil {
				child = &treeNode{children: map[string]*treeNode{}}
				n.children[p] = child
			}
			n = child
		}
		n.children[parts[len(parts)-1]] = &treeNode{file: &f}
	}
	return hex.EncodeToString(root.hash())
}

type treeNode struct {
	file     *File
	children map[string]*treeNode
}

func (n *treeNode) hash() []byte {
	if n.file != nil {
		return gitObject("blob", n.file.Data)
	}
	// git orders tree entries by name, comparing directories as if their
	// name ended in "/".
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	key := func(name string) string {
		if n.children[name].file == nil {
			return name + "/"
		}
		return name
	}
	sort.Slice(names, func(i, j int) bool { return key(names[i]) < key(names[j]) })
	var body []byte
	for _, name := range names {
		child := n.children[name]
		mode := "40000"
		if child.file != nil {
			mode = "100644"
			if child.file.Mode&0o111 != 0 {
				mode = "100755"
			}
		}
		body = append(body, mode+" "+name+"\x00"...)
		body = append(body, child.hash()...)
	}
	return gitObject("tree", body)
}

func gitObject(kind string, data []byte) []byte {
	h := sha1.New()
	fmt.Fprintf(h, "%s %d\x00", kind, len(data))
	h.Write(data)
	return h.Sum(nil)
}

// readDir loads an installed skill directory from disk. dir itself may be a
// symlink (the `skills` CLI links agent directories to .agents/skills); .git
// and node_modules are skipped, as the `skills` CLI does when hashing.
//
// modes supplies file modes where the filesystem has none (Windows), keyed by
// relative path; it is usually the upstream skill's files.
func readDir(dir string, modes []File) ([]File, error) {
	var files []File
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && (d.Name() == ".git" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		f := File{Path: filepath.ToSlash(rel), Mode: 0o644, Data: data}
		if runtime.GOOS == "windows" {
			if i := slices.IndexFunc(modes, func(m File) bool { return m.Path == f.Path }); i >= 0 {
				f.Mode = modes[i].Mode
			}
		} else if info.Mode()&0o111 != 0 {
			f.Mode = 0o755
		}
		files = append(files, f)
		return nil
	})
	return files, err
}
