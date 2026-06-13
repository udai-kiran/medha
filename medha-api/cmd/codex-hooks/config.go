package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type config struct {
	URL     string
	Secret  string
	Project string
}

// loadConfig resolves AGENTMEMORY_URL/SECRET/PROJECT from (in order):
//  1. Environment variables
//  2. AGENTMEMORY_ENV_FILE env var
//  3. .env.mcp walked up from cwd (each level checked independently)
//  4. $HOME/.codex/.env.mcp  (Codex-specific global config location)
//  5. $XDG_CONFIG_HOME/agent-mem/.env.mcp
//  6. Defaults (URL: http://localhost:3111, Project: git-derived)
//
// Each source fills only the keys that are still unset, so a project-local
// .env.mcp with only AGENTMEMORY_PROJECT does not shadow a global
// AGENTMEMORY_SECRET from a lower-priority file.
func loadConfig(cwd string) config {
	c := config{
		URL:     os.Getenv("AGENTMEMORY_URL"),
		Secret:  os.Getenv("AGENTMEMORY_SECRET"),
		Project: os.Getenv("AGENTMEMORY_PROJECT"),
	}
	for _, f := range findEnvFiles(cwd) {
		if c.URL != "" && c.Secret != "" && c.Project != "" {
			break
		}
		pairs := parseEnvFile(f)
		if c.URL == "" {
			c.URL = pairs["AGENTMEMORY_URL"]
		}
		if c.Secret == "" {
			c.Secret = pairs["AGENTMEMORY_SECRET"]
		}
		if c.Project == "" {
			c.Project = pairs["AGENTMEMORY_PROJECT"]
		}
	}
	if c.URL == "" {
		c.URL = "http://localhost:3111"
	}
	if c.Project == "" {
		c.Project = deriveProject(cwd)
	}
	return c
}

// findEnvFiles returns all candidate env files in descending priority order.
// Callers merge values across files, stopping once all keys are satisfied.
func findEnvFiles(cwd string) []string {
	var files []string
	if f := os.Getenv("AGENTMEMORY_ENV_FILE"); f != "" {
		if fileExists(f) {
			files = append(files, f)
		}
	}
	for d := cwd; ; {
		if p := filepath.Join(d, ".env.mcp"); fileExists(p) {
			files = append(files, p)
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	home := os.Getenv("HOME")
	if home != "" {
		if p := filepath.Join(home, ".codex", ".env.mcp"); fileExists(p) {
			files = append(files, p)
		}
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" && home != "" {
		xdg = filepath.Join(home, ".config")
	}
	if xdg != "" {
		if p := filepath.Join(xdg, "agent-mem", ".env.mcp"); fileExists(p) {
			files = append(files, p)
		}
	}
	return files
}

func parseEnvFile(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	m := make(map[string]string)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

func deriveProject(cwd string) string {
	remote, _ := gitOut(cwd, "remote", "get-url", "origin")
	branch, _ := gitOut(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	if remote != "" && branch != "" {
		return remote + "__" + branch
	}
	root, err := gitOut(cwd, "rev-parse", "--show-toplevel")
	base := filepath.Base(cwd)
	if err == nil && root != "" {
		base = filepath.Base(root)
	}
	if branch != "" {
		return base + "__" + branch
	}
	return base
}

func gitOut(cwd string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", cwd}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
