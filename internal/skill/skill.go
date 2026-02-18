package skill

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/chatbotkit/pantalk/internal/config"
)

const defaultRepo = "https://github.com/pantalk/skills.git"

// skillsSubdir was previously used to namespace pantalk skills under a
// subdirectory (e.g. .github/skills/pantalk/). Now that skill names are
// self-namespaced with a "pantalk-" prefix, skills install directly into
// the agent's skills directory (e.g. .github/skills/pantalk-install/).
const skillsSubdir = ""

var defaultCachePath = config.DefaultSkillsCachePath()

// agentTarget describes where a particular AI agent expects to find skills.
type agentTarget struct {
	Name    string // human-readable agent name
	Project string // directory relative to project root
	User    string // directory under $HOME
}

// knownAgents lists the AI agent skill directory conventions that pantalk
// knows how to install into.
var knownAgents = []agentTarget{
	{Name: "github", Project: ".github/skills", User: ".copilot/skills"},
	{Name: "cursor", Project: ".cursor/skills", User: ".cursor/skills"},
	{Name: "claude", Project: ".claude/skills", User: ".claude/skills"},
	{Name: "codex", Project: ".codex/skills", User: ".codex/skills"},
}

// Run dispatches the skill subcommand.
func Run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	subcommand := args[0]
	subArgs := args[1:]

	switch subcommand {
	case "install":
		return runInstall(subArgs)
	case "update":
		return runUpdate(subArgs)
	case "list":
		return runList(subArgs)
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown skill command %q", subcommand)
	}
}

func runInstall(args []string) error {
	flags := flag.NewFlagSet("skill install", flag.ContinueOnError)
	cache := flags.String("cache", defaultCachePath, "local cache directory for the skills repository")
	repo := flags.String("repo", defaultRepo, "git repository URL to clone")
	scope := flags.String("scope", "project", "install scope: project, user, or all")
	agents := flags.String("agents", "", "comma-separated agent targets (github,cursor,claude,codex); empty = auto-detect")
	dryRun := flags.Bool("dry-run", false, "show what would be installed without writing files")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if !gitAvailable() {
		return errors.New("git is required to install skills - please install git and try again")
	}

	// Step 1: Clone or update the skills repo cache.
	cachePath := strings.TrimSpace(*cache)
	if err := ensureCache(cachePath, strings.TrimSpace(*repo)); err != nil {
		return err
	}

	// Step 2: Discover skills from the cache.
	skills, err := discoverSkills(cachePath)
	if err != nil {
		return fmt.Errorf("discover skills in cache: %w", err)
	}
	if len(skills) == 0 {
		return errors.New("no skills found in the repository")
	}

	// Step 3: Resolve target directories.
	targets, err := resolveTargets(*scope, *agents)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return errors.New("no agent directories found - use --scope user to install into home directory or --agents to specify targets")
	}

	// Step 4: Copy skills into each target directory.
	installed := 0
	for _, target := range targets {
		dest := filepath.Join(target, skillsSubdir)
		if *dryRun {
			fmt.Printf("[dry-run] would install %d skills into %s\n", len(skills), dest)
			continue
		}
		if err := copySkills(cachePath, skills, dest); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to install into %s: %v\n", dest, err)
			continue
		}
		fmt.Printf("installed %d skills into %s\n", len(skills), dest)
		installed++
	}

	if *dryRun {
		return nil
	}

	if installed == 0 {
		return errors.New("failed to install skills into any target directory")
	}

	return nil
}

func runUpdate(args []string) error {
	flags := flag.NewFlagSet("skill update", flag.ContinueOnError)
	cache := flags.String("cache", defaultCachePath, "local cache directory for the skills repository")
	scope := flags.String("scope", "project", "update scope: project, user, or all")
	agents := flags.String("agents", "", "comma-separated agent targets; empty = auto-detect")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if !gitAvailable() {
		return errors.New("git is required to update skills")
	}

	cachePath := strings.TrimSpace(*cache)

	if !isGitRepo(cachePath) {
		return fmt.Errorf("no skills cache found at %s - run 'skill install' first", cachePath)
	}

	if err := gitPull(cachePath); err != nil {
		return err
	}

	skills, err := discoverSkills(cachePath)
	if err != nil {
		return fmt.Errorf("discover skills in cache: %w", err)
	}

	targets, err := resolveTargets(*scope, *agents)
	if err != nil {
		return err
	}

	updated := 0
	for _, target := range targets {
		dest := filepath.Join(target, skillsSubdir)
		if err := copySkills(cachePath, skills, dest); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to update %s: %v\n", dest, err)
			continue
		}
		fmt.Printf("updated %d skills in %s\n", len(skills), dest)
		updated++
	}

	if updated == 0 && len(targets) > 0 {
		return errors.New("failed to update skills in any target directory")
	}

	return nil
}

func runList(args []string) error {
	flags := flag.NewFlagSet("skill list", flag.ContinueOnError)
	cache := flags.String("cache", defaultCachePath, "local cache directory for the skills repository")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cachePath := strings.TrimSpace(*cache)

	if !dirExists(cachePath) {
		return errors.New("skills not installed - run 'pantalk skill install' first")
	}

	skills, err := discoverSkills(cachePath)
	if err != nil {
		return err
	}

	if len(skills) == 0 {
		fmt.Println("no skills found")
		return nil
	}

	fmt.Println("available skills:")
	for _, s := range skills {
		fmt.Printf("  %s\t%s\n", s.Name, s.File)
	}

	fmt.Println()

	// Also show where skills are currently installed.
	targets := findExistingTargets("project")
	targets = append(targets, findExistingTargets("user")...)

	if len(targets) > 0 {
		fmt.Println("installed locations:")
		for _, t := range targets {
			dest := filepath.Join(t, skillsSubdir)
			if dirExists(dest) {
				fmt.Printf("  %s\n", dest)
			}
		}
	}

	return nil
}

// resolveTargets returns the list of absolute directory paths where skills
// should be installed based on the scope and agents filter.
func resolveTargets(scope string, agentsFilter string) ([]string, error) {
	allowedAgents := parseCSV(agentsFilter)

	switch scope {
	case "project":
		return filterAgents(findExistingTargets("project"), allowedAgents), nil
	case "user":
		return filterAgents(findExistingTargets("user"), allowedAgents), nil
	case "all":
		targets := findExistingTargets("project")
		targets = append(targets, findExistingTargets("user")...)
		return filterAgents(targets, allowedAgents), nil
	default:
		return nil, fmt.Errorf("unknown scope %q (expected project, user, or all)", scope)
	}
}

// findExistingTargets returns directories that exist for the given scope.
// For "project" scope, it also creates the skills subdirectory if the parent
// agent directory exists (e.g. if .github/ exists, .github/skills/ is valid).
func findExistingTargets(scope string) []string {
	var targets []string

	for _, agent := range knownAgents {
		var candidate string
		switch scope {
		case "project":
			root := findProjectRoot()
			if root == "" {
				continue
			}
			// Check if the agent's parent directory exists in the project.
			// For example, if ".github" exists, ".github/skills" is a valid
			// target even if the skills subdirectory doesn't exist yet.
			parentDir := filepath.Join(root, filepath.Dir(agent.Project))
			if agent.Project == filepath.Dir(agent.Project) {
				// No parent directory component - use project root directly.
				parentDir = root
			}
			agentDir := filepath.Join(root, strings.Split(agent.Project, "/")[0])
			if !dirExists(agentDir) && !dirExists(parentDir) {
				continue
			}
			candidate = filepath.Join(root, agent.Project)
		case "user":
			home := os.Getenv("HOME")
			if home == "" {
				continue
			}
			candidate = filepath.Join(home, agent.User)
			// For user scope, the parent directory must exist.
			agentDir := filepath.Join(home, strings.Split(agent.User, "/")[0])
			if !dirExists(agentDir) {
				continue
			}
		}

		if candidate != "" {
			targets = append(targets, candidate)
		}
	}

	return targets
}

func filterAgents(targets []string, allowed []string) []string {
	if len(allowed) == 0 {
		return targets
	}

	allowSet := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		allowSet[a] = true
	}

	var filtered []string
	for _, t := range targets {
		for _, agent := range knownAgents {
			if allowSet[agent.Name] && (strings.Contains(t, agent.Project) || strings.Contains(t, agent.User)) {
				filtered = append(filtered, t)
				break
			}
		}
	}
	return filtered
}

// SkillEntry represents a discovered skill on disk.
type SkillEntry struct {
	Name string // directory name (e.g. "send-message")
	File string // relative path to SKILL.md
	Dir  string // relative path to the skill directory
}

// discoverSkills walks the cache directory looking for SKILL.md files.
func discoverSkills(root string) ([]SkillEntry, error) {
	var skills []SkillEntry

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip the .git directory.
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}

		if d.IsDir() {
			return nil
		}

		if strings.ToUpper(d.Name()) == "SKILL.MD" {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}

			dir := filepath.Dir(rel)
			name := filepath.Base(dir)
			if name == "." {
				name = filepath.Base(path)
			}

			skills = append(skills, SkillEntry{Name: name, File: rel, Dir: dir})
		}

		return nil
	})

	return skills, err
}

// copySkills copies each skill directory from the cache into the destination.
func copySkills(cacheRoot string, skills []SkillEntry, dest string) error {
	for _, s := range skills {
		srcDir := filepath.Join(cacheRoot, s.Dir)
		dstDir := filepath.Join(dest, s.Name)

		if err := os.MkdirAll(dstDir, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", dstDir, err)
		}

		if err := copyDir(srcDir, dstDir); err != nil {
			return fmt.Errorf("copy skill %s: %w", s.Name, err)
		}
	}

	return nil
}

// copyDir recursively copies the contents of src into dst.
func copyDir(src string, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}

		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		return copyFile(path, target)
	})
}

// copyFile copies a single file from src to dst.
func copyFile(src string, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	return out.Close()
}

// ensureCache clones the skills repo into cachePath if not present, or pulls
// latest if already cached.
func ensureCache(cachePath string, repoURL string) error {
	if isGitRepo(cachePath) {
		fmt.Printf("updating skills cache at %s\n", cachePath)
		return gitPull(cachePath)
	}

	if dirExists(cachePath) {
		entries, _ := os.ReadDir(cachePath)
		if len(entries) > 0 {
			return fmt.Errorf("cache directory %s exists but is not a git repository - remove it and retry", cachePath)
		}
	}

	return gitClone(repoURL, cachePath)
}

// findProjectRoot walks up from the current working directory looking for a
// .git directory to find the project root.
func findProjectRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		if isGitRepo(dir) {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func isGitRepo(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && info.IsDir()
}

func dirExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

func gitClone(repo string, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}

	fmt.Printf("cloning %s into %s\n", repo, dest)

	cmd := exec.Command("git", "clone", "--depth", "1", repo, dest)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone failed: %w", err)
	}

	return nil
}

func gitPull(dir string) error {
	cmd := exec.Command("git", "-C", dir, "pull", "--ff-only")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git pull failed: %w", err)
	}

	return nil
}

func parseCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func printUsage() {
	fmt.Print(`pantalk skill commands

Usage:
  pantalk skill install [--scope project|user|all] [--agents github,cursor,claude,codex] [--repo URL] [--dry-run]
  pantalk skill update  [--scope project|user|all] [--agents github,cursor,claude,codex]
  pantalk skill list

The install command clones the pantalk skills repository and copies skill
files into the appropriate AI agent directories:

  Project-level (relative to git root):
    .github/skills/    (GitHub Copilot)
    .cursor/skills/    (Cursor)
    .claude/skills/    (Claude)
    .codex/skills/     (Codex)

  User-level (home directory):
    ~/.copilot/skills/ (GitHub Copilot)
    ~/.cursor/skills/  (Cursor)
    ~/.claude/skills/  (Claude)
    ~/.codex/skills/   (Codex)

Skill names are prefixed with "pantalk-" (e.g. pantalk-install, pantalk-send-message)
so they can coexist with skills from other tools.

By default, only existing agent directories are targeted. Use --agents to
limit to specific agents or --scope to choose between project and user level.

Flags:
  --scope    project (default), user, or all
  --agents   comma-separated list: github, cursor, claude, codex
  --repo     override skills repository URL
  --dry-run  show what would be installed without writing files
`)
}
