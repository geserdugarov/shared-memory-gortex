package indexer

import (
	"os"
	"path/filepath"
	"strings"
)

// WorktreeInfo describes a directory's relationship to its git
// repository.
type WorktreeInfo struct {
	// IsWorktree is true when the directory is a linked git worktree
	// rather than the repository's main checkout.
	IsWorktree bool
	// MainRepoPath is the main worktree's root — the shared base that
	// every linked worktree of the repo descends from. It equals the
	// queried path for a main checkout or a non-git directory.
	MainRepoPath string
	// GitCommonDir is the shared .git directory all of a repo's
	// worktrees use. Empty when the directory is not a git repository.
	GitCommonDir string
}

// ResolveWorktree reports whether path is a linked git worktree and
// resolves the main repository it shares a .git directory with.
//
// A linked worktree carries a `.git` *file* (`gitdir: <path>`) instead
// of a directory; the referenced per-worktree gitdir holds a
// `commondir` file pointing back at the shared .git, whose parent is
// the main checkout. A git submodule also uses a `.git` file but has
// no `commondir`, so it resolves to itself — a submodule is a separate
// repository, not a worktree. A main checkout or a non-git directory
// likewise resolves to itself.
//
// Keying the index cache by MainRepoPath lets every worktree of one
// repo share a base identity; combined with branch-keyed snapshots
// each worktree still gets its own slot.
func ResolveWorktree(path string) WorktreeInfo {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	info := WorktreeInfo{MainRepoPath: abs}

	gitPath := filepath.Join(abs, ".git")
	st, err := os.Stat(gitPath)
	if err != nil {
		return info // not a git repository
	}
	if st.IsDir() {
		info.GitCommonDir = gitPath
		return info // the main checkout
	}

	// `.git` is a file: a linked worktree or a submodule. Read the
	// gitdir indirection.
	content, err := os.ReadFile(gitPath)
	if err != nil {
		return info
	}
	line := strings.TrimSpace(string(content))
	wtGitDir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if wtGitDir == "" || wtGitDir == line {
		return info // malformed .git file
	}
	if !filepath.IsAbs(wtGitDir) {
		wtGitDir = filepath.Join(abs, wtGitDir)
	}

	// Only a worktree's gitdir carries a `commondir` file; a
	// submodule's does not. Its absence means "not a worktree".
	commonRaw, err := os.ReadFile(filepath.Join(wtGitDir, "commondir"))
	if err != nil {
		return info
	}
	common := strings.TrimSpace(string(commonRaw))
	if !filepath.IsAbs(common) {
		common = filepath.Join(wtGitDir, common)
	}
	common = filepath.Clean(common)

	info.IsWorktree = true
	info.GitCommonDir = common
	// The main checkout is the directory that contains the shared .git.
	if filepath.Base(common) == ".git" {
		info.MainRepoPath = filepath.Dir(common)
	}
	return info
}
