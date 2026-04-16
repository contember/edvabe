package template

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Sentinel errors so HTTP handlers can discriminate without string
// matching.
var (
	// ErrNotFound is returned for an unknown template ID or alias.
	ErrNotFound = errors.New("template: not found")
	// ErrAliasTaken is returned when creating a template with an alias
	// already claimed by another template.
	ErrAliasTaken = errors.New("template: alias already in use")
)

// Clock is a tiny injection point so unit tests can control CreatedAt /
// Build.StartedAt without wall-clock sleeping. Mirrors the Clock in
// internal/sandbox/manager.go.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Store is a mutex-guarded, JSON-file-backed registry of templates.
//
// Reads use an RWMutex so concurrent lookups from sandbox create don't
// serialize. Writes take the full lock and flush synchronously to disk
// so a crash between Create and the next read cannot strand a template
// in memory-only state.
type Store struct {
	path  string
	clock Clock

	mu        sync.RWMutex
	templates map[string]*Template
	aliases   map[string]string // alias → templateID
}

// Options configures NewStore.
type Options struct {
	// Path is the JSON file the store persists to. If empty, an
	// in-memory store is used (useful for tests).
	Path string
	// Clock is injected for deterministic timestamps in tests. Defaults
	// to wall-clock time.
	Clock Clock
}

// NewStore constructs a Store and loads any existing templates from
// disk. A missing file is not an error — the store starts empty. A
// malformed file IS an error; callers decide whether to bail or move it
// aside.
func NewStore(opts Options) (*Store, error) {
	if opts.Clock == nil {
		opts.Clock = realClock{}
	}
	s := &Store{
		path:      opts.Path,
		clock:     opts.Clock,
		templates: make(map[string]*Template),
		aliases:   make(map[string]string),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// diskFormat is the on-disk JSON schema. A thin wrapper so we can add
// schema metadata later without breaking old stores.
type diskFormat struct {
	Version   int         `json:"version"`
	Templates []*Template `json:"templates"`
}

const diskFormatVersion = 1

func (s *Store) load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("template store: read %q: %w", s.path, err)
	}
	if len(data) == 0 {
		return nil
	}
	var disk diskFormat
	if err := json.Unmarshal(data, &disk); err != nil {
		return fmt.Errorf("template store: parse %q: %w", s.path, err)
	}
	for _, t := range disk.Templates {
		if t == nil || t.ID == "" {
			continue
		}
		s.templates[t.ID] = t
		if t.Alias != "" {
			s.aliases[t.Alias] = t.ID
		}
	}
	return nil
}

// flush writes the current state to disk. Caller must hold s.mu.
func (s *Store) flush() error {
	if s.path == "" {
		return nil
	}
	disk := diskFormat{Version: diskFormatVersion, Templates: make([]*Template, 0, len(s.templates))}
	for _, t := range s.templates {
		disk.Templates = append(disk.Templates, t)
	}
	sort.Slice(disk.Templates, func(i, j int) bool {
		return disk.Templates[i].CreatedAt.Before(disk.Templates[j].CreatedAt)
	})
	data, err := json.MarshalIndent(&disk, "", "  ")
	if err != nil {
		return fmt.Errorf("template store: marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("template store: mkdir: %w", err)
	}
	tmp := s.path + ".part"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("template store: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("template store: rename: %w", err)
	}
	return nil
}

// SeedOptions describes a built-in template that edvabe registers at
// startup. Built-in templates are idempotent: if the alias already
// exists the record is updated in-place (startCmd, readyCmd, imageTag
// may have changed after a binary upgrade); if it does not exist a new
// template is created.
type SeedOptions struct {
	Alias    string
	ImageTag string
	StartCmd string
	ReadyCmd string
}

// SeedBuiltIn creates or updates a built-in template. See SeedOptions.
func (s *Store) SeedBuiltIn(opts SeedOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id, ok := s.aliases[opts.Alias]; ok {
		t := s.templates[id]
		t.ImageTag = opts.ImageTag
		t.StartCmd = opts.StartCmd
		t.ReadyCmd = opts.ReadyCmd
		return s.flush()
	}

	t := &Template{
		ID:        NewTemplateID(),
		Name:      opts.Alias,
		Alias:     opts.Alias,
		ImageTag:  opts.ImageTag,
		StartCmd:  opts.StartCmd,
		ReadyCmd:  opts.ReadyCmd,
		CreatedAt: s.clock.Now(),
		Builds: []Build{{
			ID:        NewBuildID(),
			Status:    BuildStatusReady,
			StartedAt: s.clock.Now(),
		}},
	}
	s.templates[t.ID] = t
	s.aliases[opts.Alias] = t.ID
	return s.flush()
}

// CreateOptions is the input to Store.Create. Name is required and
// doubles as the alias unless Alias is set explicitly (E2B convention:
// the `name` field on POST /v3/templates is used as both).
type CreateOptions struct {
	Name     string
	Alias    string
	Tags     []string
	CPUCount int
	MemoryMB int
}

// Create inserts a new template and flushes. Returns ErrAliasTaken if
// the chosen alias already belongs to another template.
func (s *Store) Create(opts CreateOptions) (*Template, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	alias := opts.Alias
	if alias == "" {
		alias = opts.Name
	}
	if alias != "" {
		if _, exists := s.aliases[alias]; exists {
			return nil, ErrAliasTaken
		}
	}

	t := &Template{
		ID:        NewTemplateID(),
		Name:      opts.Name,
		Tags:      append([]string(nil), opts.Tags...),
		Alias:     alias,
		CPUCount:  opts.CPUCount,
		MemoryMB:  opts.MemoryMB,
		CreatedAt: s.clock.Now(),
	}
	s.templates[t.ID] = t
	if alias != "" {
		s.aliases[alias] = t.ID
	}
	if err := s.flush(); err != nil {
		delete(s.templates, t.ID)
		if alias != "" {
			delete(s.aliases, alias)
		}
		return nil, err
	}
	return t.clone(), nil
}

// Get returns a defensive copy of the template with the given ID, or
// ErrNotFound.
func (s *Store) Get(id string) (*Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.templates[id]
	if !ok {
		return nil, ErrNotFound
	}
	return t.clone(), nil
}

// ResolveAlias returns the template whose alias matches, or
// ErrNotFound.
func (s *Store) ResolveAlias(alias string) (*Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.aliases[alias]
	if !ok {
		return nil, ErrNotFound
	}
	t, ok := s.templates[id]
	if !ok {
		return nil, ErrNotFound
	}
	return t.clone(), nil
}

// ResolveNameOrID looks up a template by ID first, then by alias. This
// is the resolver the sandbox manager uses at create time.
func (s *Store) ResolveNameOrID(idOrAlias string) (*Template, error) {
	if idOrAlias == "" {
		return nil, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if t, ok := s.templates[idOrAlias]; ok {
		return t.clone(), nil
	}
	if id, ok := s.aliases[idOrAlias]; ok {
		if t, ok := s.templates[id]; ok {
			return t.clone(), nil
		}
	}
	return nil, ErrNotFound
}

// List returns a snapshot of all templates sorted by CreatedAt ascending.
func (s *Store) List() []*Template {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Template, 0, len(s.templates))
	for _, t := range s.templates {
		out = append(out, t.clone())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// Delete removes a template. Returns ErrNotFound if the template does
// not exist.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[id]
	if !ok {
		return ErrNotFound
	}
	delete(s.templates, id)
	if t.Alias != "" && s.aliases[t.Alias] == id {
		delete(s.aliases, t.Alias)
	}
	if err := s.flush(); err != nil {
		// Best-effort restore so an on-disk write failure does not
		// lose state from memory. This keeps the store coherent even
		// when the filesystem misbehaves.
		s.templates[id] = t
		if t.Alias != "" {
			s.aliases[t.Alias] = id
		}
		return err
	}
	return nil
}

// UpdateMeta applies a mutator to the template's mutable fields under
// the write lock and flushes. The mutator must not change ID. Returns
// ErrNotFound if no such template exists, ErrAliasTaken if the mutator
// sets an alias already held by another template.
func (s *Store) UpdateMeta(id string, mutator func(*Template)) (*Template, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[id]
	if !ok {
		return nil, ErrNotFound
	}
	oldAlias := t.Alias
	mutator(t)
	if t.Alias != oldAlias {
		if t.Alias != "" {
			if existingID, exists := s.aliases[t.Alias]; exists && existingID != id {
				t.Alias = oldAlias
				return nil, ErrAliasTaken
			}
			s.aliases[t.Alias] = id
		}
		if oldAlias != "" {
			delete(s.aliases, oldAlias)
		}
	}
	if err := s.flush(); err != nil {
		return nil, err
	}
	return t.clone(), nil
}

// AppendBuild inserts a new Build entry onto the template and flushes.
// Returns the cloned build so callers can log its ID.
func (s *Store) AppendBuild(templateID string, build Build) (*Build, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[templateID]
	if !ok {
		return nil, ErrNotFound
	}
	t.Builds = append(t.Builds, build)
	if err := s.flush(); err != nil {
		t.Builds = t.Builds[:len(t.Builds)-1]
		return nil, err
	}
	b := t.Builds[len(t.Builds)-1]
	return &b, nil
}

// UpdateBuild finds a build by ID and runs the mutator on it under the
// write lock. Returns ErrNotFound if either the template or the build
// is unknown.
func (s *Store) UpdateBuild(templateID, buildID string, mutator func(*Build)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[templateID]
	if !ok {
		return ErrNotFound
	}
	for i := range t.Builds {
		if t.Builds[i].ID == buildID {
			mutator(&t.Builds[i])
			return s.flush()
		}
	}
	return ErrNotFound
}

// clone returns a deep-ish copy of a Template so callers cannot mutate
// the store's internal state through the returned pointer. Builds and
// Tags are copied; everything else is value-type.
func (t *Template) clone() *Template {
	if t == nil {
		return nil
	}
	cp := *t
	if t.Tags != nil {
		cp.Tags = append([]string(nil), t.Tags...)
	}
	if t.Builds != nil {
		cp.Builds = append([]Build(nil), t.Builds...)
	}
	return &cp
}
