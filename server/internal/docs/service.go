package docs

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"dochub/internal/config"
	"dochub/internal/store"
)

type Service struct {
	store      *store.Store
	cfg        config.Config
	queue      chan store.SyncJob
	branchLock sync.Map
}

type downloadedRepo struct {
	barePath string
	worktree string
	commit   string
}

func NewService(db *store.Store, cfg config.Config) (*Service, error) {
	for _, dir := range []string{"repos", "snapshots", "tmp"} {
		if err := os.MkdirAll(filepath.Join(cfg.StorageDir, dir), 0o750); err != nil {
			return nil, err
		}
	}
	return &Service{
		store: db,
		cfg:   cfg,
		queue: make(chan store.SyncJob, 128),
	}, nil
}

func (s *Service) Start(ctx context.Context, workers int) {
	for range workers {
		go s.worker(ctx)
	}
}

func (s *Service) Enqueue(job store.SyncJob) {
	select {
	case s.queue <- job:
	default:
		go func() {
			message := "同步队列已满，请稍后重试"
			_ = s.store.StartJob(context.Background(), job.ID)
			_ = s.store.FailBranchSync(context.Background(), job.ProjectID, job.Branch, message)
			_ = s.store.FinishJob(context.Background(), job.ID, "failed", message)
		}()
	}
}

func (s *Service) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.queue:
			s.runJob(ctx, job)
		}
	}
}

func (s *Service) runJob(ctx context.Context, job store.SyncJob) {
	// 同一项目的多个分支共享 bare 仓库，必须串行处理以避免 clone/fetch 竞争。
	lockValue, _ := s.branchLock.LoadOrStore(job.ProjectID, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	_ = s.store.StartJob(ctx, job.ID)
	_ = s.store.MarkBranchRunning(ctx, job.ProjectID, job.Branch)
	err := s.SyncBranch(ctx, job.ProjectID, job.Branch)
	if err == nil {
		_ = s.store.FinishJob(ctx, job.ID, "succeeded", "")
		return
	}
	message := err.Error()
	_ = s.store.FailBranchSync(ctx, job.ProjectID, job.Branch, message)
	_ = s.store.FinishJob(ctx, job.ID, "failed", message)
}

func (s *Service) SyncBranch(ctx context.Context, projectID, branch string) error {
	if err := ValidateBranch(branch); err != nil {
		return err
	}
	project, err := s.store.Project(ctx, projectID)
	if err != nil {
		return err
	}
	if err := ValidateRepoURL(project.RepoURL, s.cfg.GitHostAllowlist); err != nil {
		return err
	}
	docRoots, err := NormalizeDocRoots(project.DocRoots)
	if err != nil {
		return err
	}

	repo, err := s.prepareRepo(ctx, project.ID, project.RepoURL, branch)
	if err != nil {
		return err
	}
	defer s.removeWorktree(repo.barePath, repo.worktree)

	snapshotID := fmt.Sprintf("%d-%s", time.Now().UnixNano(), shortHash(repo.commit))
	tempSnapshot := filepath.Join(s.cfg.StorageDir, "tmp", project.ID+"-"+shortHash(branch)+"-"+snapshotID)
	for _, docRoot := range docRoots {
		sourceRoot := filepath.Join(repo.worktree, filepath.FromSlash(docRoot))
		info, err := os.Stat(sourceRoot)
		if err != nil {
			os.RemoveAll(tempSnapshot)
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("分支 %s 中不存在文档目录 %s", branch, docRoot)
			}
			return fmt.Errorf("读取文档目录 %s 失败: %w", docRoot, err)
		}
		if !info.IsDir() {
			os.RemoveAll(tempSnapshot)
			return fmt.Errorf("文档路径 %s 不是目录", docRoot)
		}
		targetRoot := filepath.Join(tempSnapshot, filepath.FromSlash(docRoot))
		if err := copyTree(sourceRoot, targetRoot); err != nil {
			os.RemoveAll(tempSnapshot)
			return fmt.Errorf("复制文档目录 %s 失败: %w", docRoot, err)
		}
	}
	documents, err := indexMarkdown(tempSnapshot)
	if err != nil {
		os.RemoveAll(tempSnapshot)
		return fmt.Errorf("建立文档索引失败: %w", err)
	}
	if len(documents) == 0 {
		os.RemoveAll(tempSnapshot)
		return fmt.Errorf("文档目录 %s 中没有 Markdown 文件", strings.Join(docRoots, "、"))
	}

	finalSnapshot := filepath.Join(
		s.cfg.StorageDir, "snapshots", project.ID, shortHash(branch), snapshotID,
	)
	if err := os.MkdirAll(filepath.Dir(finalSnapshot), 0o750); err != nil {
		os.RemoveAll(tempSnapshot)
		return err
	}
	if err := os.Rename(tempSnapshot, finalSnapshot); err != nil {
		os.RemoveAll(tempSnapshot)
		return err
	}

	oldSnapshot, _, _ := s.store.BranchSnapshot(ctx, project.ID, branch)
	if err := s.store.FinishBranchSync(
		ctx, project.ID, branch, repo.commit, finalSnapshot, documents,
	); err != nil {
		os.RemoveAll(finalSnapshot)
		return err
	}
	if oldSnapshot != "" && oldSnapshot != finalSnapshot {
		_ = os.RemoveAll(oldSnapshot)
	}
	return nil
}

func (s *Service) ResolveAsset(ctx context.Context, projectID, branch, relative string) (string, error) {
	snapshot, docRoots, err := s.store.BranchSnapshot(ctx, projectID, branch)
	if err != nil {
		return "", err
	}
	if snapshot == "" {
		return "", store.ErrNotFound
	}
	clean, err := cleanRelative(relative)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(snapshot, filepath.FromSlash(clean))); errors.Is(err, os.ErrNotExist) {
		// 兼容升级前只保存文档目录内容、路径中不含根目录名的快照。
		for _, docRoot := range docRoots {
			legacyPath := filepath.Join(filepath.FromSlash(docRoot), filepath.FromSlash(clean))
			if _, statErr := os.Stat(filepath.Join(snapshot, legacyPath)); statErr == nil {
				clean = filepath.ToSlash(legacyPath)
				break
			}
		}
	}
	candidate := filepath.Join(snapshot, filepath.FromSlash(clean))
	if !isWithin(snapshot, candidate) {
		return "", fmt.Errorf("资源路径越界")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", store.ErrNotFound
	}
	if !isWithin(snapshot, resolved) {
		return "", fmt.Errorf("资源路径越界")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", store.ErrNotFound
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s 是目录", clean)
	}
	return resolved, nil
}

func (s *Service) RemoveProject(projectID string) {
	_ = os.RemoveAll(filepath.Join(s.cfg.StorageDir, "repos", projectID+".git"))
	_ = os.RemoveAll(filepath.Join(s.cfg.StorageDir, "snapshots", projectID))
}

func (s *Service) RemoveSnapshot(snapshot string) {
	if snapshot != "" {
		_ = os.RemoveAll(snapshot)
	}
}

func (s *Service) prepareRepo(ctx context.Context, projectID, repoURL, branch string) (downloadedRepo, error) {
	reposDir := filepath.Join(s.cfg.StorageDir, "repos")
	barePath := filepath.Join(reposDir, projectID+".git")
	if _, err := os.Stat(barePath); errors.Is(err, os.ErrNotExist) {
		tempBare := filepath.Join(reposDir, "."+projectID+"-"+shortHash(time.Now().String()))
		_ = os.RemoveAll(tempBare)
		if output, err := s.git(ctx, "", "clone", "--mirror", "--quiet", "--", repoURL, tempBare); err != nil {
			_ = os.RemoveAll(tempBare)
			return downloadedRepo{}, gitError("克隆仓库", output, err)
		}
		if err := os.Rename(tempBare, barePath); err != nil {
			_ = os.RemoveAll(tempBare)
			return downloadedRepo{}, err
		}
	} else if err != nil {
		return downloadedRepo{}, err
	} else {
		if output, err := s.git(ctx, barePath,
			"fetch", "--prune", "--quiet", "origin", "+refs/heads/*:refs/heads/*",
		); err != nil {
			return downloadedRepo{}, gitError("拉取仓库", output, err)
		}
	}

	commitOutput, err := s.git(ctx, barePath, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return downloadedRepo{}, fmt.Errorf("分支 %s 不存在", branch)
	}
	commit := strings.TrimSpace(string(commitOutput))
	worktree := filepath.Join(s.cfg.StorageDir, "tmp", "worktree-"+projectID+"-"+shortHash(branch+"-"+commit))
	_ = os.RemoveAll(worktree)
	if output, err := s.git(ctx, barePath, "worktree", "add", "--detach", "--quiet", worktree, commit); err != nil {
		return downloadedRepo{}, gitError("创建临时工作区", output, err)
	}
	return downloadedRepo{barePath: barePath, worktree: worktree, commit: commit}, nil
}

func (s *Service) removeWorktree(barePath, worktree string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = s.git(ctx, barePath, "worktree", "remove", "--force", worktree)
	_ = os.RemoveAll(worktree)
}

func (s *Service) git(ctx context.Context, gitDir string, args ...string) ([]byte, error) {
	if gitDir != "" {
		args = append([]string{"--git-dir", gitDir}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if s.cfg.SSHKeyPath != "" || s.cfg.KnownHostsPath != "" {
		sshArgs := []string{"ssh"}
		if s.cfg.SSHKeyPath != "" {
			sshArgs = append(sshArgs, "-i", s.cfg.SSHKeyPath, "-o", "IdentitiesOnly=yes")
		}
		if s.cfg.KnownHostsPath != "" {
			sshArgs = append(sshArgs,
				"-o", "UserKnownHostsFile="+s.cfg.KnownHostsPath,
				"-o", "StrictHostKeyChecking=yes",
			)
		}
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND="+strings.Join(sshArgs, " "))
	}
	return cmd.CombinedOutput()
}

func ValidateRepoURL(raw string, allowlist []string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n\x00") || strings.HasPrefix(raw, "-") {
		return fmt.Errorf("仓库地址格式无效")
	}
	var host string
	if strings.HasPrefix(raw, "ssh://") {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Hostname() == "" || parsed.Path == "" || parsed.Path == "/" {
			return fmt.Errorf("SSH 仓库地址格式无效")
		}
		host = parsed.Hostname()
	} else {
		at := strings.Index(raw, "@")
		colon := strings.Index(raw, ":")
		if at <= 0 || colon <= at+1 || colon == len(raw)-1 || strings.Contains(raw[:colon], "/") {
			return fmt.Errorf("仅支持 SSH 仓库地址，例如 git@host:team/repo.git")
		}
		host = raw[at+1 : colon]
	}
	host = strings.ToLower(strings.TrimSpace(host))
	for _, allowed := range allowlist {
		if host == normalizeAllowedHost(allowed) {
			return nil
		}
	}
	return fmt.Errorf("Git 主机 %s 不在 GIT_HOST_ALLOWLIST 中", host)
}

func normalizeAllowedHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil {
			return strings.ToLower(parsed.Hostname())
		}
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(value, "[]")
}

func ValidateDocRoot(raw string) error {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if raw == "" || strings.HasPrefix(raw, "/") {
		return fmt.Errorf("文档目录必须是仓库内的相对路径")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(raw)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("文档目录不能指向仓库外部")
	}
	return nil
}

func NormalizeDocRoots(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("至少配置一个文档目录")
	}
	if len(values) > 10 {
		return nil, fmt.Errorf("单个项目最多配置 10 个文档目录")
	}
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
		if err := ValidateDocRoot(value); err != nil {
			return nil, err
		}
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
		if seen[clean] {
			return nil, fmt.Errorf("文档目录不能重复: %s", clean)
		}
		seen[clean] = true
		result = append(result, clean)
	}
	for index, docRoot := range result {
		for nestedIndex, nestedRoot := range result {
			if index != nestedIndex && strings.HasPrefix(nestedRoot, docRoot+"/") {
				return nil, fmt.Errorf("文档目录不能互相包含: %s 与 %s", docRoot, nestedRoot)
			}
		}
	}
	return result, nil
}

func ValidateBranch(branch string) error {
	if branch == "" || len(branch) > 200 || strings.HasPrefix(branch, "-") ||
		strings.HasPrefix(branch, ".") || strings.HasSuffix(branch, "/") ||
		strings.HasSuffix(branch, ".") || strings.Contains(branch, "..") ||
		strings.Contains(branch, "@{") || strings.ContainsAny(branch, " ~^:?*[\\\x00\r\n") {
		return fmt.Errorf("分支名称无效")
	}
	parts := strings.Split(branch, "/")
	for _, part := range parts {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return fmt.Errorf("分支名称无效")
		}
	}
	return nil
}

func cleanRelative(raw string) (string, error) {
	raw = strings.ReplaceAll(raw, "\\", "/")
	if raw == "" || strings.HasPrefix(raw, "/") || strings.ContainsRune(raw, '\x00') {
		return "", fmt.Errorf("路径无效")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(raw)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("路径越界")
	}
	return clean, nil
}

func indexMarkdown(root string) ([]store.DocFile, error) {
	var documents []store.DocFile
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > 5<<20 {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		markdown := string(raw)
		documents = append(documents, store.DocFile{
			Path:      filepath.ToSlash(relative),
			Title:     extractTitle(markdown, entry.Name()),
			Markdown:  markdown,
			PlainText: markdownToPlainText(markdown),
		})
		return nil
	})
	return documents, err
}

func copyTree(source, target string) error {
	if err := os.MkdirAll(target, 0o750); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o750)
		}
		sourceFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer sourceFile.Close()
		targetFile, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(targetFile, sourceFile)
		closeErr := targetFile.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func isWithin(root, candidate string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(rootAbs, candidateAbs)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func extractTitle(markdown, fileName string) string {
	re := regexp.MustCompile(`(?m)^\s*#\s+(.+?)\s*#*\s*$`)
	if match := re.FindStringSubmatch(markdown); len(match) > 1 {
		return strings.TrimSpace(replaceInlineMarkdown(match[1]))
	}
	return strings.TrimSuffix(fileName, filepath.Ext(fileName))
}

func markdownToPlainText(markdown string) string {
	markdown = regexp.MustCompile("(?s)```.*?```").ReplaceAllString(markdown, " ")
	markdown = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`).ReplaceAllString(markdown, " ")
	markdown = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`).ReplaceAllString(markdown, "$1")
	markdown = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s*`).ReplaceAllString(markdown, "")
	markdown = regexp.MustCompile(`[`+"`*_>#|"+`]+`).ReplaceAllString(markdown, " ")
	markdown = regexp.MustCompile(`\s+`).ReplaceAllString(markdown, " ")
	return strings.TrimSpace(markdown)
}

func replaceInlineMarkdown(value string) string {
	value = regexp.MustCompile(`\*\*|__|[*_`+"`"+`]`).ReplaceAllString(value, "")
	return strings.TrimSpace(value)
}

func shortHash(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func gitError(action string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if len(message) > 800 {
		message = message[len(message)-800:]
	}
	if message == "" {
		message = err.Error()
	}
	return fmt.Errorf("%s失败: %s", action, message)
}
