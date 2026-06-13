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
//  3. .env.mcp walked up from cwd
//  4. $HOME/.codex/.env.mcp  (Codex-specific global config location)
//  5. $XDG_CONFIG_HOME/agent-mem/.env.mcp
//  6. Defaults (URL: http://localhost:3111, Project: git-derived)
func loadConfig(cwd string) config {
	c := config{
		URL:     os.Getenv("AGENTMEMORY_URL"),
		Secret:  os.Getenv("AGENTMEMORY_SECRET"),
		Project: os.Getenv("AGENTMEMORY_PROJECT"),
	}
	if c.URL == "" || c.Secret == "" || c.Project == "" {
		if envFile := findEnvFile(cwd); envFile != "" {
			pairs := parseEnvFile(envFile)
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
	}
	if c.URL == "" {
		c.URL = "http://localhost:3111"
	}
	if c.Project == "" {
		c.Project = deriveProject(cwd)
	}
	return c
}

func findEnvFile(cwd string) string {
	if f := os.Getenv("AGENTMEMORY_ENV_FILE"); f != "" {
		if _, err := os.Stat(f); err == nil {
			return f
		}
	}
	// Walk up from cwd
	for d := cwd; ; {
		if p := filepath.Join(d, ".env.mcp"); fileExists(p) {
			return p
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	// Codex global config location
	home := os.Getenv("HOME")
	if home != "" {
		if p := filepath.Join(home, ".codex", ".env.mcp"); fileExists(p) {
			return p
		}
	}
	// XDG fallback
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" && home != "" {
		xdg = filepath.Join(home, ".config")
	}
	if xdg != "" {
		if p := filepath.Join(xdg, "agent-mem", ".env.mcp"); fileExists(p) {
			return p
		}
	}
	return ""
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
