package provider

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
)

const testBranch = "master"

func TestValidateAuthConfig(t *testing.T) {
	tests := map[string]struct {
		auth    authConfig
		wantErr string
	}{
		"empty": {
			auth: authConfig{},
		},
		"https complete": {
			auth: authConfig{Username: "user", Token: "token"},
		},
		"token without username": {
			auth:    authConfig{Token: "token"},
			wantErr: "username must be configured",
		},
		"username without token": {
			auth:    authConfig{Username: "user"},
			wantErr: "token must be configured",
		},
		"ssh passphrase without key": {
			auth:    authConfig{SSHPassphrase: "pass"},
			wantErr: "ssh_private_key must be configured",
		},
		"host key options without key": {
			auth:    authConfig{KnownHostsFile: "known_hosts"},
			wantErr: "ssh_private_key must be configured",
		},
		"known hosts and insecure": {
			auth:    authConfig{SSHPrivateKey: "key", KnownHostsFile: "known_hosts", InsecureIgnoreHostKey: true},
			wantErr: "mutually exclusive",
		},
		"http and ssh": {
			auth:    authConfig{Username: "user", Token: "token", SSHPrivateKey: "key"},
			wantErr: "mutually exclusive",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := validateAuthConfig(tt.auth)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateAuthConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateAuthConfig() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestCloneManagerReadFile(t *testing.T) {
	remoteURL := newBareRemote(t, map[string]string{"README.md": "hello\n"})
	manager := newCloneManager(filepath.Join(t.TempDir(), "cache"), authConfig{}, "")

	info, err := manager.ReadFile(remoteURL, testBranch, "README.md")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if info.Content != "hello\n" {
		t.Fatalf("ReadFile() content = %q", info.Content)
	}
	if info.CommitSHA == "" || info.BlobSHA != gitBlobSHA("hello\n") {
		t.Fatalf("ReadFile() metadata = %+v", info)
	}

	_, err = manager.ReadFile(remoteURL, testBranch, "missing.txt")
	if !errors.Is(err, errGitFileNotFound) {
		t.Fatalf("ReadFile() missing error = %v, want %v", err, errGitFileNotFound)
	}
}

func TestCloneManagerReusesSyncedRepositoryForReads(t *testing.T) {
	remoteURL := newBareRemote(t, map[string]string{
		"a.txt": "a1\n",
		"b.txt": "b1\n",
	})
	manager := newCloneManager(filepath.Join(t.TempDir(), "cache"), authConfig{}, "")

	first, err := manager.ReadFile(remoteURL, testBranch, "a.txt")
	if err != nil {
		t.Fatalf("ReadFile(a.txt) error = %v", err)
	}

	externalCommit(t, remoteURL, "b.txt", "b2\n")

	second, err := manager.ReadFile(remoteURL, testBranch, "b.txt")
	if err != nil {
		t.Fatalf("ReadFile(b.txt) error = %v", err)
	}
	if second.Content != "b1\n" {
		t.Fatalf("ReadFile(b.txt) content = %q, want cached content %q", second.Content, "b1\n")
	}
	if second.CommitSHA != first.CommitSHA {
		t.Fatalf("ReadFile(b.txt) commit = %q, want initial synced commit %q", second.CommitSHA, first.CommitSHA)
	}
}

func TestCloneManagerWriteUpdateNoopAndDelete(t *testing.T) {
	remoteURL := newBareRemote(t, map[string]string{"README.md": "base\n"})
	manager := newCloneManager(filepath.Join(t.TempDir(), "cache"), authConfig{}, "")
	initialCommits := countRemoteCommits(t, remoteURL)

	createResult, err := manager.WriteFile(remoteURL, testBranch, "dir/file.txt", "one\n", nil, true, testCommitOptions("create"))
	if err != nil {
		t.Fatalf("WriteFile(create) error = %v", err)
	}
	if !createResult.Changed || createResult.CommitSHA == "" || createResult.BlobSHA != gitBlobSHA("one\n") {
		t.Fatalf("WriteFile(create) result = %+v", createResult)
	}
	if got := readRemoteFile(t, remoteURL, "dir/file.txt"); got != "one\n" {
		t.Fatalf("remote content after create = %q", got)
	}
	if got := countRemoteCommits(t, remoteURL); got != initialCommits+1 {
		t.Fatalf("commit count after create = %d, want %d", got, initialCommits+1)
	}

	expected := "one\n"
	noopResult, err := manager.WriteFile(remoteURL, testBranch, "dir/file.txt", "one\n", &expected, true, testCommitOptions("noop"))
	if err != nil {
		t.Fatalf("WriteFile(noop) error = %v", err)
	}
	if noopResult.Changed {
		t.Fatalf("WriteFile(noop) changed = true")
	}
	if got := countRemoteCommits(t, remoteURL); got != initialCommits+1 {
		t.Fatalf("commit count after noop = %d, want %d", got, initialCommits+1)
	}

	updateResult, err := manager.WriteFile(remoteURL, testBranch, "dir/file.txt", "two\n", &expected, true, testCommitOptions("update"))
	if err != nil {
		t.Fatalf("WriteFile(update) error = %v", err)
	}
	if !updateResult.Changed || updateResult.BlobSHA != gitBlobSHA("two\n") {
		t.Fatalf("WriteFile(update) result = %+v", updateResult)
	}
	if got := readRemoteFile(t, remoteURL, "dir/file.txt"); got != "two\n" {
		t.Fatalf("remote content after update = %q", got)
	}
	if got := countRemoteCommits(t, remoteURL); got != initialCommits+2 {
		t.Fatalf("commit count after update = %d, want %d", got, initialCommits+2)
	}

	expected = "two\n"
	deleteResult, err := manager.DeleteFile(remoteURL, testBranch, "dir/file.txt", &expected, testCommitOptions("delete"))
	if err != nil {
		t.Fatalf("DeleteFile() error = %v", err)
	}
	if !deleteResult.Changed || deleteResult.CommitSHA == "" {
		t.Fatalf("DeleteFile() result = %+v", deleteResult)
	}
	if got := countRemoteCommits(t, remoteURL); got != initialCommits+3 {
		t.Fatalf("commit count after delete = %d, want %d", got, initialCommits+3)
	}
	if _, err := manager.ReadFile(remoteURL, testBranch, "dir/file.txt"); !errors.Is(err, errGitFileNotFound) {
		t.Fatalf("ReadFile(deleted) error = %v, want %v", err, errGitFileNotFound)
	}
}

func TestCloneManagerDriftFailure(t *testing.T) {
	remoteURL := newBareRemote(t, map[string]string{"file.txt": "state\n"})
	manager := newCloneManager(filepath.Join(t.TempDir(), "cache"), authConfig{}, "")

	externalCommit(t, remoteURL, "file.txt", "remote\n")

	expected := "state\n"
	_, err := manager.WriteFile(remoteURL, testBranch, "file.txt", "desired\n", &expected, true, testCommitOptions("update"))
	if err == nil || !strings.Contains(err.Error(), "changed outside Terraform") {
		t.Fatalf("WriteFile() drift error = %v", err)
	}
	if got := readRemoteFile(t, remoteURL, "file.txt"); got != "remote\n" {
		t.Fatalf("remote content after rejected drift update = %q", got)
	}
}

func TestCloneManagerCreateRefusesExistingFile(t *testing.T) {
	remoteURL := newBareRemote(t, map[string]string{"file.txt": "edited\n"})
	manager := newCloneManager(filepath.Join(t.TempDir(), "cache"), authConfig{}, "")
	initialCommits := countRemoteCommits(t, remoteURL)

	_, err := manager.WriteFile(remoteURL, testBranch, "file.txt", "template\n", nil, false, testCommitOptions("create"))
	if err == nil || !strings.Contains(err.Error(), "already exists with different content") {
		t.Fatalf("WriteFile() create error = %v", err)
	}
	if got := readRemoteFile(t, remoteURL, "file.txt"); got != "edited\n" {
		t.Fatalf("remote content after refused create = %q", got)
	}

	sameResult, err := manager.WriteFile(remoteURL, testBranch, "file.txt", "edited\n", nil, false, testCommitOptions("create same"))
	if err != nil {
		t.Fatalf("WriteFile() create with matching content error = %v", err)
	}
	if sameResult.Changed {
		t.Fatalf("WriteFile() create with matching content changed = true")
	}
	if got := countRemoteCommits(t, remoteURL); got != initialCommits {
		t.Fatalf("commit count = %d, want %d", got, initialCommits)
	}

	if _, err := manager.WriteFile(remoteURL, testBranch, "file.txt", "template\n", nil, true, testCommitOptions("overwrite")); err != nil {
		t.Fatalf("WriteFile() overwrite error = %v", err)
	}
	if got := readRemoteFile(t, remoteURL, "file.txt"); got != "template\n" {
		t.Fatalf("remote content after overwrite = %q", got)
	}
}

func TestCloneManagerSharedCloneSerializesOperations(t *testing.T) {
	remoteURL := newBareRemote(t, map[string]string{"README.md": "base\n"})
	manager := newCloneManager(filepath.Join(t.TempDir(), "cache"), authConfig{}, "")
	initialCommits := countRemoteCommits(t, remoteURL)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for path, content := range map[string]string{
		"a.txt": "a\n",
		"b.txt": "b\n",
	} {
		wg.Add(1)
		go func(path, content string) {
			defer wg.Done()
			_, err := manager.WriteFile(remoteURL, testBranch, path, content, nil, true, testCommitOptions("write "+path))
			errs <- err
		}(path, content)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent WriteFile() error = %v", err)
		}
	}

	if got := readRemoteFile(t, remoteURL, "a.txt"); got != "a\n" {
		t.Fatalf("a.txt = %q", got)
	}
	if got := readRemoteFile(t, remoteURL, "b.txt"); got != "b\n" {
		t.Fatalf("b.txt = %q", got)
	}
	if got := countRemoteCommits(t, remoteURL); got != initialCommits+2 {
		t.Fatalf("commit count = %d, want %d", got, initialCommits+2)
	}
	if got := len(manager.clones); got != 1 {
		t.Fatalf("clone count = %d, want 1", got)
	}
}

const testPushBranch = "sync"

func TestCloneManagerPushBranchCreatedFromBranch(t *testing.T) {
	remoteURL := newBareRemote(t, map[string]string{"README.md": "base\n"})
	manager := newCloneManager(filepath.Join(t.TempDir(), "cache"), authConfig{}, testPushBranch)
	initialCommits := countRemoteCommits(t, remoteURL)

	info, err := manager.ReadFile(remoteURL, testBranch, "README.md")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if info.Content != "base\n" {
		t.Fatalf("ReadFile() content = %q, want branch content", info.Content)
	}
	if remoteBranchExists(t, remoteURL, testPushBranch) {
		t.Fatalf("push branch exists after read")
	}

	if _, err := manager.WriteFile(remoteURL, testBranch, "a.txt", "a\n", nil, true, testCommitOptions("create a")); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if got := readRemoteBranchFile(t, remoteURL, testPushBranch, "a.txt"); got != "a\n" {
		t.Fatalf("push branch a.txt = %q", got)
	}
	if got := countRemoteBranchCommits(t, remoteURL, testPushBranch); got != initialCommits+1 {
		t.Fatalf("push branch commit count = %d, want %d", got, initialCommits+1)
	}
	if got := countRemoteCommits(t, remoteURL); got != initialCommits {
		t.Fatalf("branch commit count = %d, want unchanged %d", got, initialCommits)
	}
}

func TestCloneManagerPushBranchReusedWhenPresent(t *testing.T) {
	remoteURL := newBareRemote(t, map[string]string{"README.md": "base\n"})
	initialCommits := countRemoteCommits(t, remoteURL)

	first := newCloneManager(filepath.Join(t.TempDir(), "cache"), authConfig{}, testPushBranch)
	if _, err := first.WriteFile(remoteURL, testBranch, "README.md", "staged\n", nil, true, testCommitOptions("stage")); err != nil {
		t.Fatalf("WriteFile(stage) error = %v", err)
	}

	second := newCloneManager(filepath.Join(t.TempDir(), "cache"), authConfig{}, testPushBranch)
	info, err := second.ReadFile(remoteURL, testBranch, "README.md")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if info.Content != "staged\n" {
		t.Fatalf("ReadFile() content = %q, want push branch content", info.Content)
	}

	expected := "staged\n"
	if _, err := second.WriteFile(remoteURL, testBranch, "README.md", "again\n", &expected, true, testCommitOptions("again")); err != nil {
		t.Fatalf("WriteFile(again) error = %v", err)
	}
	if got := countRemoteBranchCommits(t, remoteURL, testPushBranch); got != initialCommits+2 {
		t.Fatalf("push branch commit count = %d, want %d", got, initialCommits+2)
	}
	if got := readRemoteFile(t, remoteURL, "README.md"); got != "base\n" {
		t.Fatalf("branch README.md = %q, want untouched", got)
	}
}

func TestCloneManagerRetriesRejectedPush(t *testing.T) {
	for name, pushBranch := range map[string]string{
		"branch":      "",
		"push branch": testPushBranch,
	} {
		t.Run(name, func(t *testing.T) {
			remoteURL := newBareRemote(t, map[string]string{"README.md": "base\n"})
			manager := newCloneManager(filepath.Join(t.TempDir(), "cache"), authConfig{}, pushBranch)
			work := manager.workBranch(testBranch)

			if _, err := manager.WriteFile(remoteURL, testBranch, "a.txt", "a\n", nil, true, testCommitOptions("create a")); err != nil {
				t.Fatalf("WriteFile(a.txt) error = %v", err)
			}
			externalBranchCommit(t, remoteURL, work, "b.txt", "b\n")

			if _, err := manager.WriteFile(remoteURL, testBranch, "c.txt", "c\n", nil, true, testCommitOptions("create c")); err != nil {
				t.Fatalf("WriteFile(c.txt) error = %v", err)
			}
			for repoPath, want := range map[string]string{"a.txt": "a\n", "b.txt": "b\n", "c.txt": "c\n"} {
				if got := readRemoteBranchFile(t, remoteURL, work, repoPath); got != want {
					t.Fatalf("%s = %q, want %q", repoPath, got, want)
				}
			}
		})
	}
}

func TestCloneManagerRetryRechecksDrift(t *testing.T) {
	remoteURL := newBareRemote(t, map[string]string{"file.txt": "state\n"})
	manager := newCloneManager(filepath.Join(t.TempDir(), "cache"), authConfig{}, "")

	if _, err := manager.ReadFile(remoteURL, testBranch, "file.txt"); err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	externalCommit(t, remoteURL, "file.txt", "remote\n")

	expected := "state\n"
	_, err := manager.WriteFile(remoteURL, testBranch, "file.txt", "desired\n", &expected, true, testCommitOptions("update"))
	if err == nil || !strings.Contains(err.Error(), "changed outside Terraform") {
		t.Fatalf("WriteFile() drift error = %v", err)
	}
	if got := readRemoteFile(t, remoteURL, "file.txt"); got != "remote\n" {
		t.Fatalf("remote content after rejected drift update = %q", got)
	}
}

func newBareRemote(t *testing.T, files map[string]string) string {
	t.Helper()

	sourceDir := filepath.Join(t.TempDir(), "source")
	repo, err := gogit.PlainInit(sourceDir, false)
	if err != nil {
		t.Fatalf("PlainInit(source) error = %v", err)
	}
	if err := disableAutoSign(repo); err != nil {
		t.Fatalf("disableAutoSign(source) error = %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree() error = %v", err)
	}
	for repoPath, content := range files {
		fullPath := filepath.Join(sourceDir, filepath.FromSlash(repoPath))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		if _, err := wt.Add(repoPath); err != nil {
			t.Fatalf("Add(%q) error = %v", repoPath, err)
		}
	}
	if _, err := wt.Commit("initial", &gogit.CommitOptions{Author: testSignature()}); err != nil {
		t.Fatalf("Commit(initial) error = %v", err)
	}

	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	remoteRepo, err := gogit.PlainClone(remoteDir, &gogit.CloneOptions{URL: sourceDir, Bare: true})
	if err != nil {
		t.Fatalf("PlainClone(remote) error = %v", err)
	}
	defer func() { _ = remoteRepo.Close() }()
	return remoteDir
}

func readRemoteFile(t *testing.T, remoteURL, repoPath string) string {
	t.Helper()
	return readRemoteBranchFile(t, remoteURL, testBranch, repoPath)
}

func readRemoteBranchFile(t *testing.T, remoteURL, branch, repoPath string) string {
	t.Helper()

	cloneDir := filepath.Join(t.TempDir(), "clone")
	repo, err := gogit.PlainClone(cloneDir, &gogit.CloneOptions{
		URL:           remoteURL,
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		SingleBranch:  true,
	})
	if err != nil {
		t.Fatalf("PlainClone(read) error = %v", err)
	}
	defer func() { _ = repo.Close() }()
	content, err := os.ReadFile(filepath.Join(cloneDir, filepath.FromSlash(repoPath)))
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", repoPath, err)
	}
	return string(content)
}

func externalCommit(t *testing.T, remoteURL, repoPath, content string) {
	t.Helper()
	externalBranchCommit(t, remoteURL, testBranch, repoPath, content)
}

func externalBranchCommit(t *testing.T, remoteURL, branch, repoPath, content string) {
	t.Helper()

	cloneDir := filepath.Join(t.TempDir(), "external")
	repo, err := gogit.PlainClone(cloneDir, &gogit.CloneOptions{
		URL:           remoteURL,
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		SingleBranch:  true,
	})
	if err != nil {
		t.Fatalf("PlainClone(external) error = %v", err)
	}
	defer func() { _ = repo.Close() }()
	if err := disableAutoSign(repo); err != nil {
		t.Fatalf("disableAutoSign(external) error = %v", err)
	}
	fullPath := filepath.Join(cloneDir, filepath.FromSlash(repoPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(external) error = %v", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(external) error = %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree(external) error = %v", err)
	}
	if _, err := wt.Add(repoPath); err != nil {
		t.Fatalf("Add(external) error = %v", err)
	}
	if _, err := wt.Commit("external", &gogit.CommitOptions{Author: testSignature()}); err != nil {
		t.Fatalf("Commit(external) error = %v", err)
	}
	if err := repo.Push(&gogit.PushOptions{
		RefSpecs: []config.RefSpec{
			config.RefSpec("refs/heads/" + branch + ":refs/heads/" + branch),
		},
	}); err != nil {
		t.Fatalf("Push(external) error = %v", err)
	}
}

func remoteBranchExists(t *testing.T, remoteURL, branch string) bool {
	t.Helper()

	repo, err := gogit.PlainOpen(remoteURL)
	if err != nil {
		t.Fatalf("PlainOpen(remote) error = %v", err)
	}
	defer func() { _ = repo.Close() }()
	_, err = repo.Reference(plumbing.NewBranchReferenceName(branch), true)
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return false
	}
	if err != nil {
		t.Fatalf("Reference(%s) error = %v", branch, err)
	}
	return true
}

func countRemoteCommits(t *testing.T, remoteURL string) int {
	t.Helper()
	return countRemoteBranchCommits(t, remoteURL, testBranch)
}

func countRemoteBranchCommits(t *testing.T, remoteURL, branch string) int {
	t.Helper()

	repo, err := gogit.PlainOpen(remoteURL)
	if err != nil {
		t.Fatalf("PlainOpen(remote) error = %v", err)
	}
	defer func() { _ = repo.Close() }()
	ref, err := repo.Reference(plumbing.NewBranchReferenceName(branch), true)
	if err != nil {
		t.Fatalf("Reference(%s) error = %v", branch, err)
	}
	iter, err := repo.Log(&gogit.LogOptions{From: ref.Hash()})
	if err != nil {
		t.Fatalf("Log() error = %v", err)
	}
	defer iter.Close()
	count := 0
	err = iter.ForEach(func(*object.Commit) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("ForEach() error = %v", err)
	}
	return count
}

func testCommitOptions(message string) commitOptions {
	return commitOptions{
		Message:     message,
		AuthorName:  "Terraform Test",
		AuthorEmail: "terraform-test@example.com",
	}
}

func testSignature() *object.Signature {
	return &object.Signature{
		Name:  "Terraform Test",
		Email: "terraform-test@example.com",
		When:  time.Unix(1, 0),
	}
}
