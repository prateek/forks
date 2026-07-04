package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	root string

	upstreamRepo   string
	upstreamBranch string
	pinnedSHA      string
	upstreamURL    string

	track     []int
	hasTrack  bool
	author    string
	hasAuthor bool

	branchURL   string
	branches    []string
	hasBranches bool

	remotePatches []remotePatch
}

// remotePatch is a curl-able patch pinned by content hash. The URL is fetched
// at assembly time and rejected unless it hashes to sha256.
type remotePatch struct {
	URL    string
	SHA256 string
}

type forkTOML struct {
	Upstream struct {
		Repo   string `toml:"repo"`
		Branch string `toml:"branch"`
		SHA    string `toml:"sha"`
		URL    string `toml:"url"`
	} `toml:"upstream"`
	PRs struct {
		Track  *[]int  `toml:"track"`
		Author *string `toml:"author"`
	} `toml:"prs"`
	Branches struct {
		URL   *string   `toml:"url"`
		Track *[]string `toml:"track"`
	} `toml:"branches"`
	Patches struct {
		Remote []struct {
			URL    string `toml:"url"`
			SHA256 string `toml:"sha256"`
		} `toml:"remote"`
	} `toml:"patches"`
}

func loadConfig(root string) (*Config, error) {
	path := filepath.Join(root, ".fork", "fork.toml")
	var raw forkTOML
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg := &Config{
		root:           root,
		upstreamRepo:   raw.Upstream.Repo,
		upstreamBranch: raw.Upstream.Branch,
		pinnedSHA:      raw.Upstream.SHA,
		upstreamURL:    raw.Upstream.URL,
	}
	if cfg.upstreamBranch == "" {
		cfg.upstreamBranch = "main"
	}
	if cfg.upstreamURL == "" {
		base := os.Getenv("FORK_FORGE_URL")
		if base == "" {
			base = "https://github.com"
		}
		cfg.upstreamURL = strings.TrimRight(base, "/") + "/" + cfg.upstreamRepo + ".git"
	}
	if raw.PRs.Track != nil {
		cfg.track, cfg.hasTrack = *raw.PRs.Track, true
	}
	if raw.PRs.Author != nil {
		cfg.author, cfg.hasAuthor = *raw.PRs.Author, true
	}
	if cfg.hasTrack == cfg.hasAuthor {
		return nil, fmt.Errorf("fork.toml: exactly one of prs.track / prs.author is required")
	}
	if raw.Branches.Track != nil && len(*raw.Branches.Track) > 0 {
		cfg.branches, cfg.hasBranches = *raw.Branches.Track, true
		if raw.Branches.URL == nil || *raw.Branches.URL == "" {
			return nil, fmt.Errorf("fork.toml: [branches] track requires branches.url (the fork remote holding the branches)")
		}
		cfg.branchURL = *raw.Branches.URL
	}
	for i, rp := range raw.Patches.Remote {
		if rp.URL == "" || rp.SHA256 == "" {
			return nil, fmt.Errorf("fork.toml: patches.remote[%d] needs both url and sha256", i)
		}
		cfg.remotePatches = append(cfg.remotePatches, remotePatch{URL: rp.URL, SHA256: strings.ToLower(rp.SHA256)})
	}
	return cfg, nil
}

// remotePatchName is the stable queue/lock identity for a remote patch,
// derived from its pinned hash so it never collides with a local patch file
// and survives reordering in fork.toml.
func remotePatchName(sha string) string {
	short := sha
	if len(short) > 12 {
		short = short[:12]
	}
	return "remote:" + short
}

func isRemotePatchName(name string) bool {
	return strings.HasPrefix(name, "remote:")
}

func (c *Config) tomlPath() string   { return filepath.Join(c.root, ".fork", "fork.toml") }
func (c *Config) lockPath() string   { return filepath.Join(c.root, ".fork", "lock.json") }
func (c *Config) patchesDir() string { return filepath.Join(c.root, "patches") }
func (c *Config) rerereDir() string  { return filepath.Join(c.root, "rerere") }

func (c *Config) patchFiles() ([]string, error) {
	entries, err := os.ReadDir(c.patchesDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".patch") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func patchesDigest(c *Config) (map[string]string, error) {
	names, err := c.patchFiles()
	if err != nil {
		return nil, err
	}
	digest := map[string]string{}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(c.patchesDir(), name))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		digest[name] = hex.EncodeToString(sum[:])
	}
	// Remote patches contribute their pinned sha256 (which is the content
	// hash), so a changed pin registers as drift exactly like an edited local
	// patch file.
	for _, rp := range c.remotePatches {
		digest[remotePatchName(rp.SHA256)] = rp.SHA256
	}
	return digest, nil
}
