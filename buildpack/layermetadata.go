package buildpack

import (
	"errors"
	"fmt"
	"os"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/buildpacks/lifecycle/api"
)

type LayerMetadataFile struct {
	Data   interface{} `json:"data" toml:"metadata"`
	Build  bool        `json:"build" toml:"build"`
	Launch bool        `json:"launch" toml:"launch"`
	Cache  bool        `json:"cache" toml:"cache"`
}

func EncodeLayerMetadataFile(lmf LayerMetadataFile, path, buildpackAPI string) error {
	fh, err := os.Create(path)
	if err != nil {
		return err
	}
	defer fh.Close()

	encoders := supportedEncoderDecoders()

	for _, encoder := range encoders {
		if encoder.IsSupported(buildpackAPI) {
			return encoder.Encode(fh, lmf)
		}
	}
	return errors.New("couldn't find an encoder")
}

func DecodeLayerMetadataFile(path, buildpackAPI string) (LayerMetadataFile, string, error) { // FIXME: pass the logger and print the warning inside (instead of returning a message)
	fh, err := os.Open(path)
	if os.IsNotExist(err) {
		return LayerMetadataFile{}, "", nil
	} else if err != nil {
		return LayerMetadataFile{}, "", err
	}
	defer fh.Close()

	decoders := supportedEncoderDecoders()

	for _, decoder := range decoders {
		if decoder.IsSupported(buildpackAPI) {
			return decoder.Decode(path)
		}
	}
	return LayerMetadataFile{}, "", errors.New("couldn't find a decoder")
}

type encoderDecoder interface {
	IsSupported(buildpackAPI string) bool
	Encode(file *os.File, lmf LayerMetadataFile) error
	Decode(path string) (LayerMetadataFile, string, error)
}

func supportedEncoderDecoders() []encoderDecoder {
	return []encoderDecoder{
		&defaultEncoderDecoder{},
		&legacyEncoderDecoder{},
	}
}

type defaultEncoderDecoder struct{}

func (d *defaultEncoderDecoder) IsSupported(buildpackAPI string) bool {
	return api.MustParse(buildpackAPI).AtLeast("0.6")
}

func (d *defaultEncoderDecoder) Encode(file *os.File, lmf LayerMetadataFile) error {
	// omit the types table - all the flags are set to false
	type dataTomlFile struct {
		Data interface{} `toml:"metadata"`
	}
	dtf := dataTomlFile{Data: lmf.Data}
	return toml.NewEncoder(file).SetIndentTables(true).Encode(dtf)
}

func (d *defaultEncoderDecoder) Decode(path string) (LayerMetadataFile, string, error) {
	type typesTable struct {
		Build  bool `toml:"build"`
		Launch bool `toml:"launch"`
		Cache  bool `toml:"cache"`
	}
	type layerMetadataTomlFile struct {
		Data  interface{} `toml:"metadata"`
		Types typesTable  `toml:"types"`
	}

	var lmtf layerMetadataTomlFile

	// TODO / revisit: unfortunately now we open/read/parse the file twice
	topLevelSchemaInvalid, err := typesInTopLevel(path, []string{"build", "launch", "cache"})
	if err != nil {
		return LayerMetadataFile{}, "", err
	}
	msg := ""
	if topLevelSchemaInvalid {
		msg = fmt.Sprintf("the launch, cache and build flags should be in the types table of %s", path)
	}

	fs, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return LayerMetadataFile{}, msg, err
	}
	defer fs.Close()

	dec := toml.NewDecoder(fs)
	if err = dec.Decode(&lmtf); err != nil {
		var derr *toml.DecodeError
		if errors.As(err, &derr) {
			row, col := derr.Position()
			return LayerMetadataFile{}, msg, fmt.Errorf("%s\nerror occurred at line %d column %d", derr.String(), row, col)
		}
		return LayerMetadataFile{}, msg, err
	}

	return LayerMetadataFile{Data: lmtf.Data, Build: lmtf.Types.Build, Launch: lmtf.Types.Launch, Cache: lmtf.Types.Cache}, msg, nil
}

// typesInTopLevel performs shallow schema validation on the top level only
//
//	arguably there's room for a "toml schema validation" layer rather than trying to piecemeal this but here we are
func typesInTopLevel(path string, stuffYoureNotSposedToHave []string) (bool, error) {
	fs, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return false, err
	}
	defer fs.Close()

	python := map[string]interface{}{}
	dec := toml.NewDecoder(fs)
	if err = dec.Decode(&python); err != nil {
		return false, err
	}

	for _, key := range stuffYoureNotSposedToHave {
		_, has := python[key]
		if has {
			return true, nil
		}
	}
	return false, nil
}

type legacyEncoderDecoder struct{}

func (d *legacyEncoderDecoder) IsSupported(buildpackAPI string) bool {
	return api.MustParse(buildpackAPI).LessThan("0.6")
}

func (d *legacyEncoderDecoder) Encode(file *os.File, lmf LayerMetadataFile) error {
	return toml.NewEncoder(file).SetIndentTables(true).Encode(lmf)
}

func (d *legacyEncoderDecoder) Decode(path string) (LayerMetadataFile, string, error) {
	msg := ""
	topLevelSchemaInvalid, err := typesInTopLevel(path, []string{"types"})
	if err != nil {
		return LayerMetadataFile{}, "", err
	}
	if topLevelSchemaInvalid {
		msg = "Types table isn't supported in this buildpack api version. The launch, build and cache flags should be in the top level. Ignoring the values in the types table."
	}

	var lmf LayerMetadataFile
	fs, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return LayerMetadataFile{}, "", err
	}
	defer fs.Close()

	dec := toml.NewDecoder(fs)
	if err = dec.Decode(&lmf); err != nil {
		var derr *toml.DecodeError
		if errors.As(err, &derr) {
			row, col := derr.Position()
			return LayerMetadataFile{}, msg, fmt.Errorf("%s\nerror occurred at line %d column %d", derr.String(), row, col)
		}
		return LayerMetadataFile{}, msg, err
	}
	return lmf, msg, nil
}
