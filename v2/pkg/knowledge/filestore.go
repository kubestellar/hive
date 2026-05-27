package knowledge

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxFileSizeBytes = 512 * 1024 // skip files larger than 512KB
	indexRefreshInterval = 30 * time.Second
)

// FileStore reads markdown files from a local directory (e.g. an Obsidian vault)
// and exposes them as knowledge facts. It supports search, read, list, and stats
// operations compatible with the wiki Client interface.
type FileStore struct {
	rootDir       string
	name          string
	mu            sync.RWMutex
	pages         map[string]filePage
	lastIndexed   time.Time
	logger        *slog.Logger
	prevPageCount int
}

type filePage struct {
	Slug     string
	Title    string
	Body     string
	Tags     []string
	Path     string
	ModTime  time.Time
}

// NewFileStore creates a store that indexes markdown files under rootDir.
func NewFileStore(rootDir string, name string, logger *slog.Logger) (*FileStore, error) {
	info, err := os.Stat(rootDir)
	if err != nil {
		return nil, fmt.Errorf("vault directory %s: %w", rootDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("vault path %s is not a directory", rootDir)
	}

	s := &FileStore{
		rootDir: rootDir,
		name:    name,
		pages:   make(map[string]filePage),
		logger:  logger,
	}

	s.reindex()
	return s, nil
}

// Name returns the display name of this vault.
func (s *FileStore) Name() string { return s.name }

// RootDir returns the filesystem path.
func (s *FileStore) RootDir() string { return s.rootDir }

func (s *FileStore) reindex() {
	pages := make(map[string]filePage)

	err := filepath.WalkDir(s.rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") {
				return fs.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".md" && ext != ".markdown" {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxFileSizeBytes {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		rel, _ := filepath.Rel(s.rootDir, path)
		slug := strings.TrimSuffix(rel, ext)
		slug = strings.ReplaceAll(slug, string(filepath.Separator), "/")

		title, body, tags := parseObsidianFile(string(data), filepath.Base(slug))

		pages[slug] = filePage{
			Slug:    slug,
			Title:   title,
			Body:    body,
			Tags:    tags,
			Path:    path,
			ModTime: info.ModTime(),
		}

		return nil
	})

	if err != nil {
		s.logger.Warn("vault reindex error", "dir", s.rootDir, "error", err)
	}

	s.mu.Lock()
	prevCount := s.prevPageCount
	s.pages = pages
	s.lastIndexed = time.Now()
	s.prevPageCount = len(pages)
	s.mu.Unlock()

	if len(pages) != prevCount {
		s.logger.Info("vault indexed",
			"name", s.name,
			"dir", s.rootDir,
			"pages", len(pages),
			"page_delta", len(pages)-prevCount,
		)
	}
}

func (s *FileStore) refreshIfStale() {
	s.mu.RLock()
	stale := time.Since(s.lastIndexed) > indexRefreshInterval
	s.mu.RUnlock()
	if stale {
		s.reindex()
	}
}

// Search finds pages matching the query via simple substring matching.
func (s *FileStore) Search(query string, limit int) []Fact {
	s.refreshIfStale()
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 20
	}

	queryLower := strings.ToLower(query)
	terms := strings.Fields(queryLower)
	if len(terms) == 0 {
		return nil
	}

	type scored struct {
		page  filePage
		score float64
	}

	var matches []scored
	for _, p := range s.pages {
		titleLower := strings.ToLower(p.Title)
		bodyLower := strings.ToLower(p.Body)
		combined := titleLower + " " + bodyLower

		var score float64
		for _, term := range terms {
			if strings.Contains(titleLower, term) {
				score += 2.0
			}
			if strings.Contains(bodyLower, term) {
				score += 1.0
			}
			for _, tag := range p.Tags {
				if strings.Contains(strings.ToLower(tag), term) {
					score += 1.5
				}
			}
		}
		_ = combined

		if score > 0 {
			matches = append(matches, scored{page: p, score: score})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].score > matches[j].score
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}

	facts := make([]Fact, len(matches))
	for i, m := range matches {
		snippet := m.page.Body
		const maxSnippetLen = 200
		if len(snippet) > maxSnippetLen {
			snippet = snippet[:maxSnippetLen] + "…"
		}
		facts[i] = Fact{
			Slug:       m.page.Slug,
			Title:      m.page.Title,
			Type:       FactPattern,
			Body:       snippet,
			Confidence: m.score / float64(len(terms)*3),
			Tags:       m.page.Tags,
			Layer:      LayerPersonal,
		}
		if facts[i].Confidence > 1.0 {
			facts[i].Confidence = 1.0
		}
	}
	return facts
}

// ReadPage returns a single page by slug.
func (s *FileStore) ReadPage(slug string) (*Fact, error) {
	s.refreshIfStale()
	s.mu.RLock()
	defer s.mu.RUnlock()

	p, ok := s.pages[slug]
	if !ok {
		return nil, fmt.Errorf("page not found: %s", slug)
	}
	return &Fact{
		Slug:       p.Slug,
		Title:      p.Title,
		Type:       FactPattern,
		Body:       p.Body,
		Confidence: 0.8,
		Tags:       p.Tags,
		Layer:      LayerPersonal,
	}, nil
}

// ListPages returns all pages, optionally filtered by a tag.
func (s *FileStore) ListPages(tagFilter string) []Fact {
	s.refreshIfStale()
	s.mu.RLock()
	defer s.mu.RUnlock()

	facts := make([]Fact, 0, len(s.pages))
	for _, p := range s.pages {
		if tagFilter != "" {
			found := false
			for _, t := range p.Tags {
				if strings.EqualFold(t, tagFilter) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		snippet := p.Body
		const maxSnippetLen = 200
		if len(snippet) > maxSnippetLen {
			snippet = snippet[:maxSnippetLen] + "…"
		}
		facts = append(facts, Fact{
			Slug:       p.Slug,
			Title:      p.Title,
			Type:       FactPattern,
			Body:       snippet,
			Confidence: 0.8,
			Tags:       p.Tags,
			Layer:      LayerPersonal,
		})
	}

	sort.Slice(facts, func(i, j int) bool {
		return facts[i].Title < facts[j].Title
	})
	return facts
}

// Stats returns aggregate stats for this vault.
func (s *FileStore) Stats() FileStoreStats {
	s.refreshIfStale()
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := FileStoreStats{
		TotalPages:  len(s.pages),
		LastIndexed: s.lastIndexed,
		RootDir:     s.rootDir,
		Name:        s.name,
		TagCounts:   make(map[string]int),
	}
	for _, p := range s.pages {
		for _, t := range p.Tags {
			stats.TagCounts[t]++
		}
	}
	return stats
}

// FileStoreStats holds aggregate info about a vault.
type FileStoreStats struct {
	TotalPages  int            `json:"total_pages"`
	LastIndexed time.Time      `json:"last_indexed"`
	RootDir     string         `json:"root_dir"`
	Name        string         `json:"name"`
	TagCounts   map[string]int `json:"tag_counts"`
}

// Reindex forces a full re-scan of the vault directory.
func (s *FileStore) Reindex() {
	s.reindex()
}

// parseObsidianFile extracts title, body, and tags from an Obsidian-style markdown file.
// Supports YAML frontmatter (tags field) and inline #tags.
func parseObsidianFile(content string, fallbackTitle string) (title string, body string, tags []string) {
	title = fallbackTitle
	body = content

	if strings.HasPrefix(content, "---\n") {
		endIdx := strings.Index(content[4:], "\n---")
		if endIdx > 0 {
			frontmatter := content[4 : 4+endIdx]
			body = strings.TrimSpace(content[4+endIdx+4:])

			for _, line := range strings.Split(frontmatter, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "title:") {
					title = strings.TrimSpace(strings.TrimPrefix(line, "title:"))
					title = strings.Trim(title, "\"'")
				}
				if strings.HasPrefix(line, "tags:") {
					tagVal := strings.TrimSpace(strings.TrimPrefix(line, "tags:"))
					if strings.HasPrefix(tagVal, "[") {
						tagVal = strings.Trim(tagVal, "[]")
						for _, t := range strings.Split(tagVal, ",") {
							t = strings.TrimSpace(t)
							t = strings.Trim(t, "\"'")
							if t != "" {
								tags = append(tags, t)
							}
						}
					}
				}
				if strings.HasPrefix(line, "- ") && len(tags) > 0 {
					t := strings.TrimSpace(strings.TrimPrefix(line, "- "))
					t = strings.Trim(t, "\"'")
					if t != "" {
						tags = append(tags, t)
					}
				}
			}
		}
	}

	if strings.HasPrefix(body, "# ") {
		nlIdx := strings.Index(body, "\n")
		if nlIdx > 0 {
			title = strings.TrimSpace(body[2:nlIdx])
			body = strings.TrimSpace(body[nlIdx+1:])
		}
	}

	// Extract inline Obsidian #tags
	for _, word := range strings.Fields(body) {
		if strings.HasPrefix(word, "#") && len(word) > 1 && !strings.HasPrefix(word, "##") {
			tag := strings.Trim(word, "#.,;:!?")
			if tag != "" && !containsTag(tags, tag) {
				tags = append(tags, tag)
			}
		}
	}

	return title, body, tags
}

func containsTag(tags []string, tag string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}
