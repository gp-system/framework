package generator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"
)

// ManifestFile marks a project root and records what the generators created.
const ManifestFile = "gpsystem.yaml"

// Manifest is the generator's view of a consumer project.
type Manifest struct {
	Version int `yaml:"version"`
	// Name is the project name; it seeds TypeSpec namespaces.
	Name string `yaml:"name"`
	// Module is the project's Go module path; cross-checked against go.mod.
	Module string `yaml:"module"`
	// Kit records the kit module the project was generated against.
	Kit string `yaml:"kit"`
	// DB is the database stack the project was generated for ("pgx" or "bun");
	// empty means pgx (projects predating the choice).
	DB string `yaml:"db,omitempty"`
	// Auth names the module `add auth` generated; empty until it runs. Later
	// generators read it to wire the identity middleware into new entry
	// points, so @permission/@policy tags work without manual steps.
	Auth    string                  `yaml:"auth,omitempty"`
	Modules map[string]*ModuleEntry `yaml:"modules"`
	// Seeds are the seeder names registered in the project's central seeds/
	// package (via `add seeder`); empty for projects with no generated
	// seeders. Central rather than per-module, unlike Events/Mail/
	// Notifications, because seeds/ is a single project-level package.
	Seeds []string `yaml:"seeds,omitempty"`
}

// ModuleEntry records what a `new module` generated: its surfaces and events.
type ModuleEntry struct {
	Surfaces []string `yaml:"surfaces"`
	// Events are the event names the module owns (via `add event`); empty for
	// modules with no generated events.
	Events []string `yaml:"events,omitempty"`
	// Mail are the notifier names the module owns (via `add mail`); empty for
	// modules that send no mail.
	Mail []string `yaml:"mail,omitempty"`
	// Notifications are the notification names the module owns (via `add
	// notification`); empty for modules with no generated notifications.
	Notifications []string `yaml:"notifications,omitempty"`
	// DBs are the module's named database connections, added via `add db`;
	// empty for modules that only use the project's default connection.
	DBs []DBConnEntry `yaml:"dbs,omitempty"`
	// SurfaceDBs maps a surface name to the named connection its module
	// repository skeleton was rendered for (via `add surface --db`); surfaces
	// not present here use the default connection and a flat repository/
	// directory.
	SurfaceDBs map[string]string `yaml:"surfaceDBs,omitempty"`
}

// DBConnEntry records one named database connection added to a module via
// `add db`: its name and the connector stack ("pgx" or "bun") it uses.
type DBConnEntry struct {
	Name      string `yaml:"name"`
	Connector string `yaml:"connector"`
}

// NewManifest returns a version-1 manifest for a freshly generated project.
func NewManifest(name, module, kit string) *Manifest {
	return &Manifest{Version: 1, Name: name, Module: module, Kit: kit, Modules: map[string]*ModuleEntry{}}
}

// DBName returns the project's database stack, defaulting to "pgx" when
// unset.
func (m *Manifest) DBName() string {
	if m.DB == "" {
		return "pgx"
	}
	return m.DB
}

// AddSurface records a module/surface pair, creating the module entry as
// needed. Returns an error when the surface already exists.
func (m *Manifest) AddSurface(module, surface string) error {
	if m.Modules == nil {
		m.Modules = map[string]*ModuleEntry{}
	}
	entry := m.Modules[module]
	if entry == nil {
		entry = &ModuleEntry{}
		m.Modules[module] = entry
	}
	if slices.Contains(entry.Surfaces, surface) {
		return fmt.Errorf("module %q already has surface %q", module, surface)
	}
	entry.Surfaces = append(entry.Surfaces, surface)
	return nil
}

// AddEvent records an event under a module, creating the module entry as
// needed. Returns an error when the event already exists.
func (m *Manifest) AddEvent(module, event string) error {
	if m.Modules == nil {
		m.Modules = map[string]*ModuleEntry{}
	}
	entry := m.Modules[module]
	if entry == nil {
		entry = &ModuleEntry{}
		m.Modules[module] = entry
	}
	if slices.Contains(entry.Events, event) {
		return fmt.Errorf("module %q already has event %q", module, event)
	}
	entry.Events = append(entry.Events, event)
	return nil
}

// AddMail records a mail notifier under a module, creating the module entry
// as needed. Returns an error when the notifier already exists.
func (m *Manifest) AddMail(module, name string) error {
	if m.Modules == nil {
		m.Modules = map[string]*ModuleEntry{}
	}
	entry := m.Modules[module]
	if entry == nil {
		entry = &ModuleEntry{}
		m.Modules[module] = entry
	}
	if slices.Contains(entry.Mail, name) {
		return fmt.Errorf("module %q already has mail notifier %q", module, name)
	}
	entry.Mail = append(entry.Mail, name)
	return nil
}

// AddNotification records a notification under a module, creating the module
// entry as needed. Returns an error when the notification already exists.
func (m *Manifest) AddNotification(module, name string) error {
	if m.Modules == nil {
		m.Modules = map[string]*ModuleEntry{}
	}
	entry := m.Modules[module]
	if entry == nil {
		entry = &ModuleEntry{}
		m.Modules[module] = entry
	}
	if slices.Contains(entry.Notifications, name) {
		return fmt.Errorf("module %q already has notification %q", module, name)
	}
	entry.Notifications = append(entry.Notifications, name)
	return nil
}

// AddDB records a named database connection under a module, creating the
// module entry as needed. Returns an error when the module already has a
// connection with that name.
func (m *Manifest) AddDB(module, name, connector string) error {
	if m.Modules == nil {
		m.Modules = map[string]*ModuleEntry{}
	}
	entry := m.Modules[module]
	if entry == nil {
		entry = &ModuleEntry{}
		m.Modules[module] = entry
	}
	if _, ok := m.FindDB(module, name); ok {
		return fmt.Errorf("module %q already has a %q database connection", module, name)
	}
	entry.DBs = append(entry.DBs, DBConnEntry{Name: name, Connector: connector})
	return nil
}

// AddSeed records a seeder name in the project's central seeds/ package.
// Returns an error when a seeder with that name already exists.
func (m *Manifest) AddSeed(name string) error {
	if slices.Contains(m.Seeds, name) {
		return fmt.Errorf("a seeder named %q already exists", name)
	}
	m.Seeds = append(m.Seeds, name)
	return nil
}

// FindDB looks up a module's named database connection by name.
func (m *Manifest) FindDB(module, name string) (DBConnEntry, bool) {
	entry := m.Modules[module]
	if entry == nil {
		return DBConnEntry{}, false
	}
	for _, c := range entry.DBs {
		if c.Name == name {
			return c, true
		}
	}
	return DBConnEntry{}, false
}

// SetSurfaceDB records that a surface's repository skeleton is bound to a
// named database connection, creating the module entry as needed.
func (m *Manifest) SetSurfaceDB(module, surface, name string) {
	if m.Modules == nil {
		m.Modules = map[string]*ModuleEntry{}
	}
	entry := m.Modules[module]
	if entry == nil {
		entry = &ModuleEntry{}
		m.Modules[module] = entry
	}
	if entry.SurfaceDBs == nil {
		entry.SurfaceDBs = map[string]string{}
	}
	entry.SurfaceDBs[surface] = name
}

// FindRoot walks up from start looking for gpsystem.yaml.
func FindRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ManifestFile)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("not inside a gpsystem project (no gpsystem.yaml found; run `new project` first or cd into a project)")
		}
		dir = parent
	}
}

// LoadManifest reads and parses root's gpsystem.yaml.
func LoadManifest(root string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(root, ManifestFile))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ManifestFile, err)
	}
	if m.Version != 1 {
		return nil, fmt.Errorf("unsupported manifest version %d", m.Version)
	}
	return &m, nil
}

// Save writes the manifest to root, replacing any existing gpsystem.yaml
// atomically (via a temp file + rename) so a crash mid-write cannot corrupt
// the file that marks the project root.
func (m *Manifest) Save(root string) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	dst := filepath.Join(root, ManifestFile)
	tmp, err := os.CreateTemp(root, ManifestFile+".tmp-*")
	if err != nil {
		return fmt.Errorf("save manifest: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op once the rename below succeeds
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("save manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save manifest: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("save manifest: %w", err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return fmt.Errorf("save manifest: %w", err)
	}
	return nil
}
