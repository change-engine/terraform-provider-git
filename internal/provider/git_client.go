package provider

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gogit "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/client"
	"github.com/go-git/go-git/v6/plumbing/object"
	githttp "github.com/go-git/go-git/v6/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v6/plumbing/transport/ssh"
	"github.com/go-git/go-git/v6/plumbing/transport/ssh/knownhosts"
	cryptossh "golang.org/x/crypto/ssh"
)

var errGitFileNotFound = errors.New("git file not found")

type cloneManager struct {
	cacheDir string
	auth     authConfig

	mu     sync.Mutex
	clones map[string]*managedClone
}

type managedClone struct {
	mu  sync.Mutex
	dir string
}

type gitFileInfo struct {
	Content   string
	CommitSHA string
	BlobSHA   string
}

type gitWriteResult struct {
	Changed             bool
	CommitSHA           string
	BlobSHA             string
	LastRemoteCommitSHA string
}

type gitDeleteResult struct {
	Changed             bool
	CommitSHA           string
	LastRemoteCommitSHA string
}

type commitOptions struct {
	Message     string
	AuthorName  string
	AuthorEmail string
}

func newCloneManager(cacheDir string, auth authConfig) *cloneManager {
	return &cloneManager{
		cacheDir: cacheDir,
		auth:     auth,
		clones:   map[string]*managedClone{},
	}
}

func (m *cloneManager) withRepo(repositoryURL, branch string, fn func(*gogit.Repository, string) error) error {
	key := m.cloneKey(repositoryURL, branch)

	m.mu.Lock()
	clone, ok := m.clones[key]
	if !ok {
		clone = &managedClone{dir: filepath.Join(m.cacheDir, key)}
		m.clones[key] = clone
	}
	m.mu.Unlock()

	clone.mu.Lock()
	defer clone.mu.Unlock()

	repo, err := m.openOrClone(repositoryURL, branch, clone.dir)
	if err != nil {
		return err
	}
	if err := m.fetchAndReset(repo, branch); err != nil {
		return err
	}
	return fn(repo, clone.dir)
}

func (m *cloneManager) ReadFile(repositoryURL, branch, repoPath string) (gitFileInfo, error) {
	var info gitFileInfo
	err := m.withRepo(repositoryURL, branch, func(repo *gogit.Repository, workDir string) error {
		head, err := headSHA(repo)
		if err != nil {
			return err
		}
		content, err := readWorktreeFile(workDir, repoPath)
		if err != nil {
			return err
		}
		info = gitFileInfo{
			Content:   content,
			CommitSHA: head,
			BlobSHA:   gitBlobSHA(content),
		}
		return nil
	})
	return info, err
}

func (m *cloneManager) WriteFile(repositoryURL, branch, repoPath, desiredContent string, expectedContent *string, opts commitOptions) (gitWriteResult, error) {
	var result gitWriteResult
	err := m.withRepo(repositoryURL, branch, func(repo *gogit.Repository, workDir string) error {
		head, err := headSHA(repo)
		if err != nil {
			return err
		}
		result.LastRemoteCommitSHA = head

		currentContent, err := readWorktreeFile(workDir, repoPath)
		if err != nil && !errors.Is(err, errGitFileNotFound) {
			return err
		}
		currentExists := err == nil
		if expectedContent != nil {
			if !currentExists {
				return fmt.Errorf("remote file %q is missing; refusing to overwrite drifted state", repoPath)
			}
			if currentContent != *expectedContent {
				return fmt.Errorf("remote file %q changed outside Terraform; refusing to overwrite drifted state", repoPath)
			}
		}
		if currentExists && currentContent == desiredContent {
			result.CommitSHA = head
			result.BlobSHA = gitBlobSHA(desiredContent)
			return nil
		}

		if err := writeWorktreeFile(workDir, repoPath, desiredContent); err != nil {
			return err
		}
		commitSHA, err := stageAndCommitSinglePath(repo, repoPath, opts)
		if err != nil {
			return err
		}
		if err := pushBranch(repo, branch, m.auth); err != nil {
			return err
		}
		result.Changed = true
		result.CommitSHA = commitSHA
		result.BlobSHA = gitBlobSHA(desiredContent)
		return nil
	})
	return result, err
}

func (m *cloneManager) DeleteFile(repositoryURL, branch, repoPath string, expectedContent *string, opts commitOptions) (gitDeleteResult, error) {
	var result gitDeleteResult
	err := m.withRepo(repositoryURL, branch, func(repo *gogit.Repository, workDir string) error {
		head, err := headSHA(repo)
		if err != nil {
			return err
		}
		result.LastRemoteCommitSHA = head

		currentContent, err := readWorktreeFile(workDir, repoPath)
		if err != nil {
			if errors.Is(err, errGitFileNotFound) {
				result.CommitSHA = head
				return nil
			}
			return err
		}
		if expectedContent != nil && currentContent != *expectedContent {
			return fmt.Errorf("remote file %q changed outside Terraform; refusing to delete drifted state", repoPath)
		}

		wt, err := repo.Worktree()
		if err != nil {
			return err
		}
		if _, err := wt.Remove(normalizeRepoPath(repoPath)); err != nil {
			return err
		}
		commitSHA, err := commitStaged(repo, opts)
		if err != nil {
			return err
		}
		if err := pushBranch(repo, branch, m.auth); err != nil {
			return err
		}
		result.Changed = true
		result.CommitSHA = commitSHA
		return nil
	})
	return result, err
}

func (m *cloneManager) cloneKey(repositoryURL, branch string) string {
	hash := sha256.Sum256([]byte(repositoryURL + "\x00" + branch + "\x00" + m.auth.fingerprint()))
	return hex.EncodeToString(hash[:])
}

func (m *cloneManager) openOrClone(repositoryURL, branch, dir string) (*gogit.Repository, error) {
	repo, err := gogit.PlainOpen(dir)
	if err == nil {
		if err := disableAutoSign(repo); err != nil {
			return nil, err
		}
		return repo, nil
	}
	if !errors.Is(err, gogit.ErrRepositoryNotExists) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	repo, err = gogit.PlainClone(dir, &gogit.CloneOptions{
		URL:           repositoryURL,
		ClientOptions: authClientOptions(m.auth),
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		SingleBranch:  true,
		Progress:      io.Discard,
	})
	if err != nil {
		return nil, err
	}
	if err := disableAutoSign(repo); err != nil {
		return nil, err
	}
	return repo, nil
}

func (m *cloneManager) fetchAndReset(repo *gogit.Repository, branch string) error {
	err := repo.Fetch(&gogit.FetchOptions{
		RemoteName:    "origin",
		ClientOptions: authClientOptions(m.auth),
		RefSpecs: []config.RefSpec{
			config.RefSpec("+refs/heads/" + branch + ":refs/remotes/origin/" + branch),
		},
		Force:    true,
		Progress: io.Discard,
	})
	if err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return err
	}

	remoteRef, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", branch), true)
	if err != nil {
		return err
	}
	localRefName := plumbing.NewBranchReferenceName(branch)
	if err := repo.Storer.SetReference(plumbing.NewHashReference(localRefName, remoteRef.Hash())); err != nil {
		return err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	if err := wt.Checkout(&gogit.CheckoutOptions{Branch: localRefName, Force: true}); err != nil {
		return err
	}
	return wt.Reset(&gogit.ResetOptions{Commit: remoteRef.Hash(), Mode: gogit.HardReset})
}

func stageAndCommitSinglePath(repo *gogit.Repository, repoPath string, opts commitOptions) (string, error) {
	wt, err := repo.Worktree()
	if err != nil {
		return "", err
	}
	if _, err := wt.Add(normalizeRepoPath(repoPath)); err != nil {
		return "", err
	}
	return commitStaged(repo, opts)
}

func commitStaged(repo *gogit.Repository, opts commitOptions) (string, error) {
	if opts.AuthorName == "" {
		opts.AuthorName = "Terraform Git Provider"
	}
	if opts.AuthorEmail == "" {
		opts.AuthorEmail = "terraform-provider-git@example.com"
	}
	if opts.Message == "" {
		opts.Message = "Update git file"
	}

	wt, err := repo.Worktree()
	if err != nil {
		return "", err
	}
	hash, err := wt.Commit(opts.Message, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  opts.AuthorName,
			Email: opts.AuthorEmail,
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", err
	}
	return hash.String(), nil
}

func pushBranch(repo *gogit.Repository, branch string, auth authConfig) error {
	err := repo.Push(&gogit.PushOptions{
		RemoteName:    "origin",
		ClientOptions: authClientOptions(auth),
		RefSpecs: []config.RefSpec{
			config.RefSpec("refs/heads/" + branch + ":refs/heads/" + branch),
		},
		Progress: io.Discard,
	})
	if errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return nil
	}
	return err
}

func headSHA(repo *gogit.Repository) (string, error) {
	head, err := repo.Head()
	if err != nil {
		return "", err
	}
	return head.Hash().String(), nil
}

func readWorktreeFile(workDir, repoPath string) (string, error) {
	fullPath, err := resolveRepoPath(workDir, repoPath)
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errGitFileNotFound
		}
		return "", err
	}
	return string(content), nil
}

func writeWorktreeFile(workDir, repoPath, content string) error {
	fullPath, err := resolveRepoPath(workDir, repoPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, []byte(content), 0o644)
}

func resolveRepoPath(workDir, repoPath string) (string, error) {
	if filepath.IsAbs(repoPath) {
		return "", fmt.Errorf("path %q must be relative", repoPath)
	}
	normalized := normalizeRepoPath(repoPath)
	if normalized == "." || strings.HasPrefix(normalized, "../") || normalized == ".." {
		return "", fmt.Errorf("path %q must stay within the repository", repoPath)
	}
	return filepath.Join(workDir, filepath.FromSlash(normalized)), nil
}

func normalizeRepoPath(repoPath string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(repoPath)))
}

func gitBlobSHA(content string) string {
	hash := sha1.New()
	_, _ = fmt.Fprintf(hash, "blob %d\x00", len([]byte(content)))
	_, _ = hash.Write([]byte(content))
	return hex.EncodeToString(hash.Sum(nil))
}

func disableAutoSign(repo *gogit.Repository) error {
	cfg, err := repo.Config()
	if err != nil {
		return err
	}
	cfg.Commit.GpgSign = config.OptBoolFalse
	return repo.SetConfig(cfg)
}

func authClientOptions(auth authConfig) []client.Option {
	if auth.Username != "" || auth.Token != "" {
		return []client.Option{
			client.WithHTTPAuth(&githttp.BasicAuth{
				Username: auth.Username,
				Password: auth.Token,
			}),
		}
	}
	if auth.SSHPrivateKey == "" {
		return nil
	}
	publicKeys, err := gitssh.NewPublicKeys("git", []byte(auth.SSHPrivateKey), auth.SSHPassphrase)
	if err != nil {
		return nil
	}
	if auth.InsecureIgnoreHostKey {
		publicKeys.HostKeyCallback = cryptossh.InsecureIgnoreHostKey()
		return []client.Option{client.WithSSHAuth(publicKeys)}
	}
	if auth.KnownHostsFile != "" {
		callback, err := knownhosts.New(auth.KnownHostsFile)
		if err == nil {
			publicKeys.HostKeyCallback = callback.HostKeyCallback()
		}
	}
	return []client.Option{client.WithSSHAuth(publicKeys)}
}
