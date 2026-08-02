package main

import (
	"context"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxWalkDepth = 8

// Entry is a single wallpaper in the library.
type Entry struct {
	Path  string // absolute path
	Name  string // base name without extension
	Rel   string // path relative to its library root, for display
	Root  string // library root it was found under
	Size  int64
	MTime time.Time

	haystack string // lowercased "rel + name", precomputed for matching
}

// Library is an in-memory index of the configured wallpaper directories.
type Library struct {
	mu      sync.RWMutex
	entries []Entry
	scanned time.Time
	roots   []string
}

func NewLibrary(roots []string) *Library {
	return &Library{roots: append([]string(nil), roots...)}
}

func (l *Library) SetRoots(roots []string) {
	l.mu.Lock()
	l.roots = append([]string(nil), roots...)
	l.mu.Unlock()
}

// Scan rebuilds the index. It is cheap enough (a stat-only walk) to run on a
// timer; wallpaper libraries are small compared to a full home directory.
func (l *Library) Scan(ctx context.Context, cfg *Config) error {
	l.mu.RLock()
	roots := append([]string(nil), l.roots...)
	l.mu.RUnlock()

	var entries []Entry
	seen := make(map[string]struct{})

	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		rootDepth := strings.Count(filepath.Clean(root), string(os.PathSeparator))

		walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// Unreadable subtree: skip rather than abort the whole scan.
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if d.IsDir() {
				if path == root {
					return nil
				}
				if !cfg.Recursive {
					return fs.SkipDir
				}
				if strings.HasPrefix(d.Name(), ".") {
					return fs.SkipDir
				}
				if strings.Count(filepath.Clean(path), string(os.PathSeparator))-rootDepth >= maxWalkDepth {
					return fs.SkipDir
				}
				return nil
			}

			if !cfg.allowedExt(d.Name()) {
				return nil
			}

			abs := path
			// Resolve symlinked entries so duplicates collapse.
			if resolved, err := filepath.EvalSymlinks(path); err == nil {
				abs = resolved
			}
			if _, dup := seen[abs]; dup {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			seen[abs] = struct{}{}

			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = filepath.Base(path)
			}
			name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

			entries = append(entries, Entry{
				Path:     abs,
				Name:     name,
				Rel:      rel,
				Root:     root,
				Size:     fi.Size(),
				MTime:    fi.ModTime(),
				haystack: strings.ToLower(rel + " " + prettify(name)),
			})
			return nil
		})
		if walkErr != nil && ctx.Err() != nil {
			return ctx.Err()
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Rel == entries[j].Rel {
			return entries[i].Path < entries[j].Path
		}
		return entries[i].Rel < entries[j].Rel
	})

	l.mu.Lock()
	l.entries = entries
	l.scanned = time.Now()
	l.mu.Unlock()
	return nil
}

func (l *Library) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.entries)
}

func (l *Library) All() []Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]Entry(nil), l.entries...)
}

// Lookup finds an entry by absolute path.
func (l *Library) Lookup(path string) (Entry, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, e := range l.entries {
		if e.Path == path {
			return e, true
		}
	}
	return Entry{}, false
}

// Random returns a random entry, avoiding `exclude` when the library has more
// than one wallpaper.
func (l *Library) Random(exclude string) (Entry, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if len(l.entries) == 0 {
		return Entry{}, false
	}
	if len(l.entries) == 1 {
		return l.entries[0], true
	}
	for i := 0; i < 16; i++ {
		e := l.entries[rand.Intn(len(l.entries))]
		if e.Path != exclude {
			return e, true
		}
	}
	return l.entries[rand.Intn(len(l.entries))], true
}

type scored struct {
	Entry Entry
	Score float64
}

// Search returns the best `limit` matches. An empty query lists the library.
func (l *Library) Search(query string, limit int) []scored {
	l.mu.RLock()
	entries := l.entries
	defer l.mu.RUnlock()

	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(query)))

	out := make([]scored, 0, min(limit, len(entries)))
	if len(tokens) == 0 {
		for i, e := range entries {
			if i >= limit {
				break
			}
			// Slight bias towards recently added wallpapers.
			out = append(out, scored{Entry: e, Score: 0.5})
		}
		return out
	}

	matches := make([]scored, 0, 64)
	for _, e := range entries {
		total := 0.0
		ok := true
		for _, t := range tokens {
			s, hit := matchToken(e.haystack, t)
			if !hit {
				ok = false
				break
			}
			total += s
		}
		if !ok {
			continue
		}
		matches = append(matches, scored{Entry: e, Score: total / float64(len(tokens))})
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return matches[i].Entry.Rel < matches[j].Entry.Rel
		}
		return matches[i].Score > matches[j].Score
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

// matchToken scores a single token against a lowercased haystack.
// Contiguous substrings always beat subsequence matches.
func matchToken(haystack, token string) (float64, bool) {
	if token == "" {
		return 0.5, true
	}
	if idx := strings.Index(haystack, token); idx >= 0 {
		// Reward early matches and matches that cover most of the name.
		position := 1.0 - float64(idx)/float64(len(haystack)+1)
		coverage := float64(len(token)) / float64(len(haystack))
		return 0.6 + 0.25*position + 0.15*coverage, true
	}

	// Subsequence fallback: every rune of the token appears in order.
	hi := 0
	first, last := -1, -1
	h := []rune(haystack)
	for _, tr := range token {
		found := false
		for hi < len(h) {
			if h[hi] == tr {
				if first < 0 {
					first = hi
				}
				last = hi
				hi++
				found = true
				break
			}
			hi++
		}
		if !found {
			return 0, false
		}
	}
	span := last - first + 1
	compactness := float64(len([]rune(token))) / float64(max(span, 1))
	return 0.15 + 0.4*compactness, true
}

// prettify turns "cyber_city-02" into "cyber city 02" for nicer labels and
// more forgiving matching.
func prettify(name string) string {
	replacer := strings.NewReplacer("_", " ", "-", " ", ".", " ")
	fields := strings.Fields(replacer.Replace(name))
	return strings.Join(fields, " ")
}
