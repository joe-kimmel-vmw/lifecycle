// Data Format Files for the buildpack api spec (https://github.com/buildpacks/spec/blob/main/buildpack.md#data-format).

package buildpack

import (
	"errors"
	"fmt"
	"os"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/buildpacks/lifecycle/api"
	"github.com/buildpacks/lifecycle/launch"
	"github.com/buildpacks/lifecycle/layers"
)

// launch.toml

type LaunchTOML struct {
	BOM       []BOMEntry
	Labels    []Label
	Processes []ProcessEntry `toml:"processes"`
	Slices    []layers.Slice `toml:"slices"`
}

// LaunchTOMLBeforeV9 exists so we can maintain backwards compaitibility forever
type LaunchTOMLBeforeV9 struct {
	BOM       []BOMEntry
	Labels    []Label
	Processes []ProcessEntryBeforeV9 `toml:"processes"`
	Slices    []layers.Slice         `toml:"slices"`
}

// ProcessEntryBeforeV9 exists only for reading old files; we will shim this into the newer format by making a []string{RawCommandValue}
type ProcessEntryBeforeV9 struct {
	Type             string   `toml:"type" json:"type"`
	Command          []string `toml:"-"` // ignored
	RawCommandValue  string   `toml:"command" json:"command"`
	Args             []string `toml:"args" json:"args"`
	Direct           *bool    `toml:"direct" json:"direct"`
	Default          bool     `toml:"default,omitempty" json:"default,omitempty"`
	WorkingDirectory string   `toml:"working-dir,omitempty" json:"working-dir,omitempty"`
}

type ProcessEntry struct {
	Type             string   `toml:"type" json:"type"`
	Command          []string `toml:"-"` // ignored
	RawCommandValue  []string `toml:"command" json:"command"`
	Args             []string `toml:"args" json:"args"`
	Direct           *bool    `toml:"direct" json:"direct"`
	Default          bool     `toml:"default,omitempty" json:"default,omitempty"`
	WorkingDirectory string   `toml:"working-dir,omitempty" json:"working-dir,omitempty"`
}

// DecodeLaunchTOML reads a launch.toml file
func DecodeLaunchTOML(launchPath string, bpAPI string, launchTOML *LaunchTOML) error {
	// decode the common bits
	fs, err := os.Open(launchPath)
	if err != nil {
		return err
	}
	defer fs.Close() // serious question - should the defer be above the err!=nil block?
	dec := toml.NewDecoder(fs)
	// decode the process.commands, which differ based on buildpack API
	commandsAreStrings := api.MustParse(bpAPI).LessThan("0.9")
	if commandsAreStrings {
		ltb := LaunchTOMLBeforeV9{}
		if err = dec.Decode(&ltb); err != nil {
			var derr *toml.DecodeError
			if errors.As(err, &derr) {
				row, col := derr.Position()
				return fmt.Errorf("%s\nerror occurred at line %d column %d", derr.String(), row, col)
			}
			return err
		}
		// TODO refactor into a method to hide our shame but not actually decrease it.
		launchTOML.BOM = ltb.BOM
		launchTOML.Labels = ltb.Labels
		launchTOML.Slices = ltb.Slices
		for _, proc := range ltb.Processes {
			np := ProcessEntry{}
			np.Args = proc.Args
			np.Command = proc.Command
			np.Default = proc.Default
			np.Direct = proc.Direct
			np.Type = proc.Type
			np.WorkingDirectory = proc.WorkingDirectory
			if len(proc.RawCommandValue) > 0 {
				np.RawCommandValue = []string{proc.RawCommandValue}
			}
			launchTOML.Processes = append(launchTOML.Processes, np)
		}
	} else {
		if err = dec.Decode(launchTOML); err != nil {
			var derr *toml.DecodeError
			if errors.As(err, &derr) {
				row, col := derr.Position()
				return fmt.Errorf("%s\nerror occurred at line %d column %d", derr.String(), row, col)
			}
			return err
		}
	}

	// processes are defined differently depending on API version
	// and will be decoded into different values
	for i, process := range launchTOML.Processes {
		if commandsAreStrings { // by now it's really "commandsWereStrings" but that's cool.
			// legacy Direct defaults to false
			if process.Direct == nil {
				direct := false
				launchTOML.Processes[i].Direct = &direct
			}
			launchTOML.Processes[i].Command = process.RawCommandValue
		} else {
			// direct is no longer allowed as a key
			if process.Direct != nil {
				return fmt.Errorf("process.direct is not supported on this buildpack version")
			}
			launchTOML.Processes[i].Command = process.RawCommandValue
		}
	}

	return nil
}

// ToLaunchProcess converts a buildpack.ProcessEntry to a launch.Process
func (p *ProcessEntry) ToLaunchProcess(bpID string) launch.Process {
	// legacy processes will always have a value
	// new processes will have a nil value but are always direct processes
	var direct bool
	if p.Direct == nil {
		direct = true
	} else {
		direct = *p.Direct
	}

	return launch.Process{
		Type:             p.Type,
		Command:          launch.NewRawCommand(p.Command),
		Args:             p.Args,
		Direct:           direct, // launch.Process requires a value
		Default:          p.Default,
		BuildpackID:      bpID,
		WorkingDirectory: p.WorkingDirectory,
	}
}

// converts launch.toml processes to launch.Processes
func (lt LaunchTOML) ToLaunchProcessesForBuildpack(bpID string) []launch.Process {
	var processes []launch.Process
	for _, process := range lt.Processes {
		processes = append(processes, process.ToLaunchProcess(bpID))
	}
	return processes
}

type BOMEntry struct {
	Require
	Buildpack GroupElement `toml:"buildpack" json:"buildpack"`
}

func (bom *BOMEntry) ConvertMetadataToVersion() {
	if version, ok := bom.Metadata["version"]; ok {
		metadataVersion := fmt.Sprintf("%v", version)
		bom.Version = metadataVersion
	}
}

func (bom *BOMEntry) convertVersionToMetadata() {
	if bom.Version != "" {
		if bom.Metadata == nil {
			bom.Metadata = make(map[string]interface{})
		}
		bom.Metadata["version"] = bom.Version
		bom.Version = ""
	}
}

type Require struct {
	Name     string                 `toml:"name" json:"name"`
	Version  string                 `toml:"version,omitempty" json:"version,omitempty"`
	Metadata map[string]interface{} `toml:"metadata" json:"metadata"`
}

func (r *Require) convertMetadataToVersion() {
	if version, ok := r.Metadata["version"]; ok {
		r.Version = fmt.Sprintf("%v", version)
	}
}

func (r *Require) ConvertVersionToMetadata() {
	if r.Version != "" {
		if r.Metadata == nil {
			r.Metadata = make(map[string]interface{})
		}
		r.Metadata["version"] = r.Version
		r.Version = ""
	}
}

func (r *Require) hasDoublySpecifiedVersions() bool {
	if _, ok := r.Metadata["version"]; ok {
		return r.Version != ""
	}
	return false
}

func (r *Require) hasInconsistentVersions() bool {
	if version, ok := r.Metadata["version"]; ok {
		return r.Version != "" && r.Version != version
	}
	return false
}

func (r *Require) hasTopLevelVersions() bool {
	return r.Version != ""
}

type Label struct {
	Key   string `toml:"key"`
	Value string `toml:"value"`
}

// build.toml

type BuildTOML struct {
	BOM   []BOMEntry `toml:"bom"`
	Unmet []Unmet    `toml:"unmet"`
}

type Unmet struct {
	Name string `toml:"name"`
}

// store.toml

type StoreTOML struct {
	Data map[string]interface{} `json:"metadata" toml:"metadata"`
}

// build plan

type BuildPlan struct {
	PlanSections
	Or planSectionsList `toml:"or"`
}

func (p *PlanSections) hasInconsistentVersions() bool {
	for _, req := range p.Requires {
		if req.hasInconsistentVersions() {
			return true
		}
	}
	return false
}

func (p *PlanSections) hasDoublySpecifiedVersions() bool {
	for _, req := range p.Requires {
		if req.hasDoublySpecifiedVersions() {
			return true
		}
	}
	return false
}

func (p *PlanSections) hasTopLevelVersions() bool {
	for _, req := range p.Requires {
		if req.hasTopLevelVersions() {
			return true
		}
	}
	return false
}

func (p *PlanSections) hasRequires() bool {
	return len(p.Requires) > 0
}

type planSectionsList []PlanSections

func (p *planSectionsList) hasInconsistentVersions() bool {
	for _, planSection := range *p {
		if planSection.hasInconsistentVersions() {
			return true
		}
	}
	return false
}

func (p *planSectionsList) hasDoublySpecifiedVersions() bool {
	for _, planSection := range *p {
		if planSection.hasDoublySpecifiedVersions() {
			return true
		}
	}
	return false
}

func (p *planSectionsList) hasTopLevelVersions() bool {
	for _, planSection := range *p {
		if planSection.hasTopLevelVersions() {
			return true
		}
	}
	return false
}

func (p *planSectionsList) hasRequires() bool {
	for _, planSection := range *p {
		if planSection.hasRequires() {
			return true
		}
	}
	return false
}

type PlanSections struct {
	Requires []Require `toml:"requires"`
	Provides []Provide `toml:"provides"`
}

type Provide struct {
	Name string `toml:"name"`
}

// buildpack plan

type Plan struct {
	Entries []Require `toml:"entries"`
}

func (p Plan) filter(unmet []Unmet) Plan {
	var out []Require
	for _, entry := range p.Entries {
		if !containsName(unmet, entry.Name) {
			out = append(out, entry)
		}
	}
	return Plan{Entries: out}
}

func (p Plan) toBOM() []BOMEntry {
	var bom []BOMEntry
	for _, entry := range p.Entries {
		bom = append(bom, BOMEntry{Require: entry})
	}
	return bom
}

func containsName(unmet []Unmet, name string) bool {
	for _, u := range unmet {
		if u.Name == name {
			return true
		}
	}
	return false
}

// layer content metadata

type LayersMetadata struct {
	ID      string                   `json:"key" toml:"key"`
	Version string                   `json:"version" toml:"version"`
	Layers  map[string]LayerMetadata `json:"layers" toml:"layers"`
	Store   *StoreTOML               `json:"store,omitempty" toml:"store"`
}

type LayerMetadata struct {
	SHA string `json:"sha" toml:"sha"`
	LayerMetadataFile
}
